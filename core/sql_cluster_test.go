//go:build keel_dqlite && cgo

package core

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/marcelsud/keel/sdk"
)

func sqlClusterAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

func TestDqliteCluster(t *testing.T) {
	if os.Getenv("KEEL_DEBUG_SQL") == "1" {
		previous := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
		defer slog.SetDefault(previous)
	}
	credentials := &Credentials{Admin: "cluster-admin-credential-000000000", Services: map[string]string{"alpha": "cluster-alpha-credential-000000000", "beta": "cluster-beta-credential-0000000000"}}
	natsAddresses := []string{sqlClusterAddress(t), sqlClusterAddress(t), sqlClusterAddress(t)}
	sqlAddresses := []string{sqlClusterAddress(t), sqlClusterAddress(t), sqlClusterAddress(t)}
	options := make([]Options, 3)
	for i := range options {
		cluster := &ClusterOptions{Name: "sql-test", Node: fmt.Sprintf("node-%d", i), Listen: natsAddresses[i], SQLListen: sqlAddresses[i], Replicas: 3}
		for j, address := range natsAddresses {
			if i != j {
				cluster.Routes = append(cluster.Routes, "nats-route://"+address)
			}
		}
		if i != 0 {
			cluster.SQLPeers = []string{sqlAddresses[0]}
		}
		options[i] = Options{Port: -1, DataDir: t.TempDir(), Credentials: credentials, SQL: true, Cluster: cluster, SQLTransactionTimeout: 5 * time.Second}
	}
	type started struct {
		i    int
		node *Core
		err  error
	}
	ready := make(chan started, 3)
	for i := range options {
		go func() { node, err := Start(options[i]); ready <- started{i, node, err} }()
	}
	nodes := make([]*Core, 3)
	t.Cleanup(func() {
		for _, node := range nodes {
			if node != nil {
				node.Close()
			}
		}
	})
	var first error
	for range 3 {
		item := <-ready
		if item.err != nil {
			first = item.err
		} else {
			nodes[item.i] = item.node
		}
	}
	if first != nil {
		t.Fatal(first)
	}
	urls := []string{nodes[0].URL(), nodes[1].URL(), nodes[2].URL()}
	connect := func(name, user, token, url string) *sdk.App {
		app, err := sdk.Connect(sdk.Options{Name: name, User: user, Token: token, URL: url})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = app.Stop() })
		return app
	}
	ctx := context.Background()
	admin := connect("alpha", AdminUser, credentials.Admin, strings.Join(urls, ","))
	files := []sdk.Migration{{Name: "0001_records.sql", SQL: "-- +goose Up\nCREATE TABLE records(id INTEGER PRIMARY KEY, body TEXT NOT NULL);"}}
	for _, service := range []string{"alpha", "beta"} {
		if err := admin.SQL.Migrate(ctx, service, files); err != nil {
			t.Fatal(err)
		}
	}
	files = append(files, sdk.Migration{Name: "0002_migration_once.sql", SQL: `-- +goose Up
CREATE TABLE migration_once (value INTEGER);
INSERT INTO migration_once VALUES (1);
CREATE TABLE trigger_budget (id INTEGER);
WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1000) INSERT INTO trigger_budget SELECT x FROM n;
-- +goose StatementBegin
CREATE TRIGGER costly_trigger AFTER UPDATE ON trigger_budget BEGIN
SELECT (WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1000000) SELECT sum(x) FROM n);
END;
-- +goose StatementEnd
`})
	migrations := make(chan error, 3)
	for _, url := range urls {
		client := connect("alpha", AdminUser, credentials.Admin, url)
		go func() { migrations <- client.SQL.Migrate(ctx, "alpha", files) }()
	}
	for range 3 {
		if err := <-migrations; err != nil {
			t.Fatal("concurrent migration", err)
		}
	}
	alpha := connect("alpha", "alpha", credentials.Services["alpha"], strings.Join(urls, ","))
	beta := connect("beta", "beta", credentials.Services["beta"], strings.Join(urls, ","))
	if result, err := alpha.SQL.Query(ctx, "SELECT * FROM migration_once"); err != nil || len(result.Rows) != 1 {
		t.Fatalf("migration did not run exactly once: %#v %v", result, err)
	}
	if _, err := alpha.SQL.Query(ctx, "INSERT INTO records VALUES (?,?)", 1, "2026-10-19"); err != nil {
		t.Fatal(err)
	}
	for _, url := range urls {
		app := connect("alpha", "alpha", credentials.Services["alpha"], url)
		result, err := app.SQL.Query(ctx, "SELECT body FROM records")
		if err != nil || len(result.Rows) != 1 || result.Rows[0]["body"] != "2026-10-19" {
			t.Fatalf("shared SQL: %#v %v", result, err)
		}
	}
	result, err := beta.SQL.Query(ctx, "SELECT * FROM records")
	if err != nil || len(result.Rows) != 0 {
		t.Fatalf("service isolation: %#v %v", result, err)
	}
	if err := alpha.SQL.Transaction(ctx, func(tx *sdk.SQLTx) error {
		_, err := tx.Query(ctx, "INSERT INTO records VALUES (?,?)", 2, "committed")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	abort := errors.New("abort")
	if err := alpha.SQL.Transaction(ctx, func(tx *sdk.SQLTx) error {
		_, err := tx.Query(ctx, "INSERT INTO records VALUES (?,?)", 3, "rollback")
		if err != nil {
			return err
		}
		return abort
	}); !errors.Is(err, abort) {
		t.Fatal(err)
	}
	for _, query := range []string{"ATTACH DATABASE '/tmp/stolen.db' AS stolen", "DELETE FROM 'goose_db_version'", "SELECT load_extension('bad')", "PRAGMA foreign_keys=OFF", "SELECT 1; DELETE FROM records"} {
		if _, err := alpha.SQL.Query(ctx, query); err == nil {
			t.Fatal("unsafe runtime SQL", query)
		}
	}
	startedAt := time.Now()
	if _, err := alpha.SQL.Query(ctx, "WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n) SELECT sum(x) FROM n"); err == nil || time.Since(startedAt) > 4*time.Second {
		t.Fatalf("unbounded SQL: %v", err)
	} else {
		t.Log("interrupted query", err, "elapsed", time.Since(startedAt))
	}
	if _, err := alpha.SQL.Query(ctx, "SELECT 1"); err != nil {
		t.Fatal("connection unusable after interruption", err)
	}
	startedAt = time.Now()
	if _, err := alpha.SQL.Query(ctx, "UPDATE trigger_budget SET id=id+1"); err == nil || time.Since(startedAt) > 4*time.Second {
		t.Fatalf("unbounded trigger execution: %v", err)
	}
	if result, err := alpha.SQL.Query(ctx, "-- query after interrupted triggers\nSELECT min(id) AS first FROM trigger_budget"); err != nil || fmt.Sprint(result.Rows[0]["first"]) != "1" {
		t.Fatalf("trigger interruption did not recover and roll back: %#v %v", result, err)
	}
	// Service passwords cannot authenticate on the private SQL replication port.
	transport, _ := clusterTLS(options[0])
	transport.Certificates = nil
	transport.ClientAuth = tls.NoClientCert
	dialer := &net.Dialer{Timeout: time.Second}
	connection, err := tls.DialWithDialer(dialer, "tcp", sqlAddresses[0], transport)
	if err == nil {
		defer connection.Close()
		connection.SetDeadline(time.Now().Add(time.Second))
		_, err = connection.Read(make([]byte, 1))
	}
	if err == nil {
		t.Fatal("SQL replication accepted a client without a cluster certificate")
	}
	leaderConnection, err := nodes[0].sql.engine.node.FindLeader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	leader, err := leaderConnection.Leader(ctx)
	leaderConnection.Close()
	if err != nil {
		t.Fatal(err)
	}
	index := -1
	for i, node := range nodes {
		if node.sql.engine.node.ID() == leader.ID {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("leader not found")
	}
	transactionReady := make(chan struct{})
	continueTransaction := make(chan struct{})
	transactionResult := make(chan error, 1)
	go func() {
		transactionResult <- alpha.SQL.Transaction(ctx, func(tx *sdk.SQLTx) error {
			if _, err := tx.Query(ctx, "INSERT INTO records VALUES (?,?)", 55, "must not survive"); err != nil {
				return err
			}
			close(transactionReady)
			<-continueTransaction
			_, err := tx.Query(ctx, "SELECT 1")
			return err
		})
	}()
	select {
	case <-transactionReady:
	case err := <-transactionResult:
		t.Fatal("begin transaction before leader failure", err)
	case <-time.After(5 * time.Second):
		t.Fatal("transaction did not start")
	}
	nodes[index].Close()
	nodes[index] = nil
	close(continueTransaction)
	if err := <-transactionResult; err == nil {
		t.Fatal("active transaction continued across SQL leader failure")
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		attempt, cancel := context.WithTimeout(ctx, time.Second)
		result, err = alpha.SQL.Query(attempt, "SELECT id FROM records ORDER BY id")
		cancel()
		if err == nil && len(result.Rows) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("SQL did not recover after leader failure: %v %#v", err, result)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := alpha.SQL.Query(ctx, "INSERT INTO records VALUES (?,?)", 4, "after failover"); err != nil {
		t.Fatal(err)
	}
	files = append(files, sdk.Migration{Name: "0003_after_failover.sql", SQL: "-- +goose Up\nINSERT INTO migration_once VALUES (2);"})
	if err := admin.SQL.Migrate(ctx, "alpha", files); err != nil {
		t.Fatal("migration after leader failover", err)
	}
	restarted, err := Start(options[index])
	if err != nil {
		t.Fatal("restart", err)
	}
	nodes[index] = restarted
	result, err = alpha.SQL.Query(ctx, "SELECT * FROM records ORDER BY id")
	if err != nil || len(result.Rows) != 3 {
		t.Fatalf("recovery: %#v %v", result, err)
	}
	for i, node := range nodes {
		if i != index {
			node.Close()
			nodes[i] = nil
		}
	}
	attempt, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if _, err := alpha.SQL.Query(attempt, "INSERT INTO records VALUES (?,?)", 5, "no quorum"); err == nil {
		t.Fatal("SQL write acknowledged without quorum")
	}
}
