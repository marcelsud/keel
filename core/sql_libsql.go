//go:build keel_libsql && !keel_dqlite && cgo

package core

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"github.com/marcelsud/keel/internal/nativelibsql"
	"github.com/marcelsud/keel/internal/protocol"
)

type sqlEngine struct{}

func (*sqlEngine) close()                            {}
func sqlRuntime(ctx context.Context) context.Context { return nativelibsql.Runtime(ctx) }
func validateRuntimeSQL(string) error                { return nil }
func validateSQLParameter(any) error                 { return nil }
func (*sqlStore) migrationEndpoints() []string       { return nil }
func (s *sqlStore) routeMigration(ctx context.Context, scope, operation string, input protocol.SQLRequest) (any, error) {
	return s.migrate(ctx, scope, input.Migrations, operation == "migrate")
}
func (s *sqlStore) openDatabases(options Options) error {
	if options.Cluster != nil {
		return errors.New("clustered SQL requires the keel_dqlite build; local libSQL databases cannot be clustered")
	}
	for name := range options.Credentials.Services {
		directory := filepath.Join(s.directory, name)
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		db, err := sql.Open("keel-libsql", filepath.Join(directory, "data.db"))
		if err != nil {
			return err
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		s.databases[name] = &serviceDB{db: db}
		var journal string
		if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&journal); err != nil {
			return err
		}
	}
	return nil
}
