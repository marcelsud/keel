//go:build keel_libsql && cgo

package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/marcelsud/keel/core"
	"github.com/marcelsud/keel/sdk"
	"github.com/nats-io/nats.go"
)

func TestNativeSQLTransactionsAndCredentials(t *testing.T) {
	credentials := &core.Credentials{Admin: "test-admin-credential-0000000000", Services: map[string]string{"alpha": "test-alpha-credential-0000000000", "beta": "test-beta-credential-00000000000"}}
	directory := t.TempDir()
	server, err := core.Start(core.Options{Port: -1, DataDir: directory, SQL: true, Credentials: credentials, SQLTransactionTimeout: 300 * time.Millisecond})
	check(t, err)
	defer server.Close()
	ctx := context.Background()
	connect := func(name, user, secret string) *sdk.App {
		a, err := sdk.Connect(sdk.Options{Name: name, User: user, Token: secret, URL: server.URL()})
		check(t, err)
		t.Cleanup(func() { _ = a.Stop() })
		return a
	}
	admin := connect("admin", core.AdminUser, credentials.Admin)
	for _, draft := range []string{"-- +goose Up\n", "-- +goose Up\n/* still editing */\n-- +goose Down\nDROP TABLE draft;"} {
		err := admin.SQL.Migrate(ctx, "alpha", []sdk.Migration{{Name: "0001_notes.sql", SQL: draft}})
		var public *sdk.Error
		if !errors.As(err, &public) || public.Code != 400 {
			t.Fatalf("empty migration was applied: %v", err)
		}
	}
	for _, service := range []string{"alpha", "beta"} {
		check(t, admin.SQL.Migrate(ctx, service, []sdk.Migration{{Name: "0001_notes.sql", SQL: "-- +goose Up\nCREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL);\nCREATE TABLE parents(id INTEGER PRIMARY KEY);\nCREATE TABLE children(id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parents(id) DEFERRABLE INITIALLY DEFERRED);"}}))
	}
	alpha := connect("alpha", "alpha", credentials.Services["alpha"])
	beta := connect("beta", "beta", credentials.Services["beta"])
	result, err := alpha.SQL.Query(ctx, "INSERT INTO notes (id,body) VALUES (?,?)", 1, "hello")
	check(t, err)
	if result.RowCount != 1 {
		t.Fatalf("insert result: %#v", result)
	}
	result, err = alpha.SQL.Query(ctx, "SELECT id,body FROM notes WHERE id = ?", 1)
	check(t, err)
	if len(result.Rows) != 1 || result.Rows[0]["body"] != "hello" {
		t.Fatal(result)
	}
	result, err = beta.SQL.Query(ctx, "SELECT * FROM notes")
	check(t, err)
	if len(result.Rows) != 0 {
		t.Fatal("service databases leaked", result)
	}
	check(t, alpha.SQL.Transaction(ctx, func(tx *sdk.SQLTx) error {
		_, err := tx.Query(ctx, "INSERT INTO notes VALUES (?,?)", 2, "committed")
		return err
	}))
	expected := errors.New("abort")
	err = alpha.SQL.Transaction(ctx, func(tx *sdk.SQLTx) error {
		_, err := tx.Query(ctx, "INSERT INTO notes VALUES (?,?)", 3, "rolled back")
		if err != nil {
			return err
		}
		return expected
	})
	if !errors.Is(err, expected) {
		t.Fatal(err)
	}
	result, err = alpha.SQL.Query(ctx, "SELECT id FROM notes ORDER BY id")
	check(t, err)
	if len(result.Rows) != 2 {
		t.Fatal(result)
	}
	for _, statement := range []string{"ATTACH DATABASE '/tmp/nope.db' AS stolen", "PRAGMA database_list", "SELECT load_extension('bad')", "SELECT 1; INSERT INTO notes VALUES (4,'bad')", "BEGIN", "DELETE FROM 'goose_db_version'", "DELETE FROM [goose_db_version]", "WITH x AS (SELECT 1) DELETE FROM `goose_db_version`", "CREATE TABLE forbidden(id)"} {
		if _, err := alpha.SQL.Query(ctx, statement); err == nil {
			t.Fatal("unsafe SQL accepted", statement)
		}
	}
	// The connection name is client-controlled; credentials must still deny beta's database.
	forged := connect("beta", "alpha", credentials.Services["alpha"])
	bounded, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err := forged.SQL.Query(bounded, "SELECT * FROM notes"); err == nil {
		t.Fatal("forged service name bypassed authorization")
	}
	if err := alpha.SQL.Migrate(bounded, "beta", []sdk.Migration{{Name: "0002_stolen.sql", SQL: "-- +goose Up\nDROP TABLE notes;"}}); err == nil {
		t.Fatal("runtime credentials ran migrations")
	}

	t.Run("values preserve storage types", func(t *testing.T) {
		result, err := alpha.SQL.Query(ctx, "SELECT CAST(? AS TEXT) AS date, ? AS literal, ? AS flag, NULL AS empty, x'00ff' AS blob, 9223372036854775807 AS large", "2026-10-19", "a\x00b", true)
		check(t, err)
		row := result.Rows[0]
		if row["date"] != "2026-10-19" || row["literal"] != "a\x00b" || row["flag"] != float64(1) || row["empty"] != nil || row["large"] != "9223372036854775807" || row["blob"].(map[string]any)["base64"] != "AP8=" {
			t.Fatal(row)
		}
	})
	t.Run("failed and expired transactions roll back", func(t *testing.T) {
		err := alpha.SQL.Transaction(ctx, func(tx *sdk.SQLTx) error {
			_, err := tx.Query(ctx, "INSERT INTO notes VALUES (?,?)", 10, "expired")
			if err != nil {
				return err
			}
			time.Sleep(450 * time.Millisecond)
			return nil
		})
		var public *sdk.Error
		if !errors.As(err, &public) || public.Code != 410 {
			t.Fatalf("expiry: %v", err)
		}
		err = alpha.SQL.Transaction(ctx, func(tx *sdk.SQLTx) error {
			_, err := tx.Query(ctx, "INSERT INTO notes VALUES (?,?)", 11, "must roll back")
			if err != nil {
				return err
			}
			_, err = tx.Query(ctx, "INSERT INTO notes VALUES (?,?)", 1, "duplicate")
			if err == nil {
				t.Fatal("constraint accepted")
			}
			return nil // Catching an error cannot commit preceding writes.
		})
		if err == nil {
			t.Fatal("failed transaction committed")
		}
		err = alpha.SQL.Transaction(ctx, func(tx *sdk.SQLTx) error {
			if _, err := tx.Query(ctx, "INSERT INTO notes VALUES (?,?)", 12, "invalid parameter rollback"); err != nil {
				return err
			}
			if _, err := tx.Query(ctx, "SELECT ?", math.NaN()); err == nil {
				t.Fatal("invalid parameter accepted")
			}
			return nil
		})
		if err == nil {
			t.Fatal("local parameter error allowed commit")
		}
		result, err := alpha.SQL.Query(ctx, "SELECT * FROM notes WHERE id IN (10,11,12)")
		check(t, err)
		if len(result.Rows) != 0 {
			t.Fatal(result)
		}
	})
	t.Run("bounded work releases connection and rolls back writes", func(t *testing.T) {
		started := time.Now()
		_, err := alpha.SQL.Query(ctx, "WITH RECURSIVE counter(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM counter) SELECT sum(x) FROM counter")
		if err == nil || time.Since(started) > 4*time.Second {
			t.Fatalf("unbounded execution: %v (%s)", err, time.Since(started))
		}
		_, err = alpha.SQL.Query(ctx, "WITH RECURSIVE n(x) AS (VALUES(100) UNION ALL SELECT x+1 FROM n WHERE x<1100) INSERT INTO notes SELECT x, 'overflow' FROM n RETURNING id")
		var public *sdk.Error
		if !errors.As(err, &public) || public.Code != 413 {
			t.Fatalf("result bound: %v", err)
		}
		result, err := alpha.SQL.Query(ctx, "SELECT * FROM notes WHERE id>=100")
		check(t, err)
		if len(result.Rows) != 0 {
			t.Fatal("oversized write committed", result)
		}
	})
	t.Run("failed commit discards connection and preserves foreign keys", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			err := alpha.SQL.Transaction(ctx, func(tx *sdk.SQLTx) error {
				_, err := tx.Query(ctx, "INSERT INTO children VALUES (?,?)", 1, 999)
				return err
			})
			var unknown *sdk.SQLCommitUnknownError
			if !errors.As(err, &unknown) {
				t.Fatalf("failed commit classification: %v", err)
			}
			result, err := alpha.SQL.Query(ctx, "SELECT * FROM children")
			check(t, err)
			if len(result.Rows) != 0 {
				t.Fatal("failed commit persisted", result)
			}
		}
	})
	t.Run("manufactured reply permissions cannot grant SQL access", func(t *testing.T) {
		nc, err := nats.Connect(server.URL(), nats.UserInfo("alpha", credentials.Services["alpha"]), nats.CustomInboxPrefix("keel.inbox.alpha"), nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) {}))
		check(t, err)
		defer nc.Close()
		sub, err := nc.SubscribeSync("keel.v1.actions.alpha.trap")
		check(t, err)
		check(t, nc.Flush())
		for _, target := range []string{"keel.v1.sql.beta.query", "keel.v1.sql.alpha.migrate"} {
			check(t, nc.PublishRequest("keel.v1.actions.alpha.trap", target, []byte("{}")))
			_, err = sub.NextMsg(time.Second)
			check(t, err)
			if _, err = nc.Request(target, []byte(`{"statement":"SELECT * FROM notes","migrations":[{"name":"0002_bad.sql","sql":"-- +goose Up\nDROP TABLE notes;"}]}`), 100*time.Millisecond); err == nil {
				t.Fatal("reply permission bypass", target)
			}
		}
		_, err = alpha.SQL.Query(ctx, "SELECT * FROM notes")
		check(t, err)
	})
}

func TestNativeSQLRestartAndAffinity(t *testing.T) {
	credentials := &core.Credentials{Admin: "test-admin-credential-0000000000", Services: map[string]string{"alpha": "test-alpha-credential-0000000000"}}
	options := core.Options{Port: -1, DataDir: t.TempDir(), SQL: true, Credentials: credentials}
	server, err := core.Start(options)
	check(t, err)
	nc, err := nats.Connect(server.URL(), nats.UserInfo(core.AdminUser, credentials.Admin))
	check(t, err)
	app, err := sdk.Connect(sdk.Options{Name: "alpha", User: core.AdminUser, Token: credentials.Admin, URL: server.URL()})
	check(t, err)
	check(t, app.SQL.Migrate(context.Background(), "alpha", []sdk.Migration{{Name: "0001_test.sql", SQL: "-- +goose Up\nCREATE TABLE records(id INTEGER);"}}))
	_, err = app.SQL.Query(context.Background(), "INSERT INTO records VALUES (?)", 1)
	check(t, err)
	message, err := nc.Request("keel.v1.sql.alpha.begin", []byte("{}"), time.Second)
	check(t, err)
	var handle struct{ ID, Subject string }
	check(t, json.Unmarshal(message.Data, &handle))
	input, _ := json.Marshal(map[string]any{"transaction": handle.ID, "statement": "INSERT INTO records VALUES (2)"})
	_, err = nc.Request(handle.Subject+".query", input, time.Second)
	check(t, err)
	nc.Close()
	check(t, app.Stop())
	server.Close()
	server, err = core.Start(options)
	check(t, err)
	defer server.Close()
	app, err = sdk.Connect(sdk.Options{Name: "alpha", User: "alpha", Token: credentials.Services["alpha"], URL: server.URL()})
	check(t, err)
	defer app.Stop()
	result, err := app.SQL.Query(context.Background(), "SELECT id FROM records")
	check(t, err)
	if len(result.Rows) != 1 || result.Rows[0]["id"] != float64(1) {
		t.Fatal(result)
	}
	nc, err = nats.Connect(server.URL(), nats.UserInfo(core.AdminUser, credentials.Admin))
	check(t, err)
	defer nc.Close()
	if _, err = nc.Request(handle.Subject+".commit", input, 100*time.Millisecond); err == nil {
		t.Fatal("old owner accepted after restart")
	}
}
