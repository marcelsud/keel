//go:build keel_dqlite && cgo

package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/antlr4-go/antlr/v4"
	dqlite "github.com/canonical/go-dqlite/v2/app"
	"github.com/canonical/go-dqlite/v2/client"
	dqliteDriver "github.com/canonical/go-dqlite/v2/driver"
	"github.com/libsql/sqlite-antlr4-parser/sqliteparser"
	"github.com/marcelsud/keel/internal/dqlitelimits"
	"github.com/marcelsud/keel/internal/protocol"
)

type sqlEngine struct {
	node *dqlite.App
}

func (e *sqlEngine) close()                          { _ = e.node.Close() }
func sqlRuntime(ctx context.Context) context.Context { return ctx }
func validateSQLParameter(value any) error {
	if text, ok := value.(string); ok && strings.ContainsRune(text, 0) {
		return protocol.BadRequest("dqlite TEXT parameters cannot contain NUL; use a BLOB expression for binary data")
	}
	return nil
}

func (s *sqlStore) openDatabases(options Options) error {
	if options.Cluster == nil {
		return errors.New("dqlite requires cluster configuration; use the keel_libsql build for local standalone SQL")
	}
	cluster := options.Cluster
	if _, port, err := net.SplitHostPort(cluster.SQLListen); err != nil || port == "" || port == "0" {
		return errors.New("SQL cluster listener requires a fixed host:port")
	}
	if err := os.MkdirAll(s.directory, 0700); err != nil {
		return err
	}
	if err := dqlitelimits.Install(); err != nil {
		return err
	}
	transport, err := clusterTLS(options)
	if err != nil {
		return err
	}
	dial := transport.Clone()
	dial.ClientAuth = 0
	dial.ClientCAs = nil // dqlite's proxy uses ClientCAs to distinguish server and client TLS.
	clusterDial := client.DialFuncWithTLS(client.DefaultDialFunc, dial)
	voters := cluster.Replicas
	if voters == 0 {
		voters = 3
	}
	// The external transport hook selects a random abstract Unix socket per node, while
	// WithTLS retains the library's authenticated replication listener. The
	// default TLS socket name otherwise collides between local bootstrap nodes.
	node, err := dqlite.New(s.directory, dqlite.WithAddress(cluster.SQLListen), dqlite.WithCluster(cluster.SQLPeers), dqlite.WithTLS(transport, dial), dqlite.WithExternalConn(clusterDial, nil), dqlite.WithVoters(voters), dqlite.WithAutoRecovery(false), dqlite.WithRolesAdjustmentFrequency(time.Second), dqlite.WithLogFunc(func(_ client.LogLevel, format string, args ...interface{}) {
		slog.Debug("dqlite", "node", cluster.Node, "message", fmt.Sprintf(format, args...))
	}))
	if err != nil {
		return err
	}
	s.engine = &sqlEngine{node: node}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := node.Ready(ctx); err != nil {
		return err
	}
	localStore := client.NewInmemNodeStore()
	if err := localStore.Set(ctx, []client.NodeInfo{{ID: node.ID(), Address: cluster.SQLListen, Role: client.Voter}}); err != nil {
		return err
	}
	localDial := client.DialFuncWithTLS(client.DefaultDialFunc, dial)
	migrations, err := dqliteDriver.New(localStore, dqliteDriver.WithConnectionTimeout(2*time.Second), dqliteDriver.WithContextTimeout(3*time.Second), dqliteDriver.WithRetryLimit(0), dqliteDriver.WithDialFunc(func(ctx context.Context, address string) (net.Conn, error) {
		if address != cluster.SQLListen {
			return nil, errors.New("migration connection cannot follow another leader")
		}
		return localDial(ctx, address)
	}))
	if err != nil {
		return err
	}
	migrationDriver := "keel-migrations-" + randomID()
	sql.Register(migrationDriver, migrations)
	for {
		leader, err := node.FindLeader(ctx)
		count := 0
		if err == nil {
			members, e := leader.Cluster(ctx)
			leader.Close()
			if e == nil {
				for _, member := range members {
					if member.Role == client.Voter {
						count++
					}
				}
			}
		}
		if count >= voters {
			break
		}
		select {
		case <-ctx.Done():
			return errors.New("SQL cluster did not form the configured voting quorum")
		case <-time.After(100 * time.Millisecond):
		}
	}
	for name := range options.Credentials.Services {
		db, err := sql.Open(node.Driver(), name+".db")
		if err != nil {
			return err
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		s.databases[name] = &serviceDB{db: db}
		migrationDB, err := sql.Open(migrationDriver, name+".db")
		if err != nil {
			return err
		}
		migrationDB.SetMaxOpenConns(1)
		migrationDB.SetMaxIdleConns(1)
		s.databases[name].migrationDB = migrationDB
		if err := db.PingContext(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqlStore) migrationEndpoints() []string {
	id := strconv.FormatUint(s.engine.node.ID(), 10)
	return []string{"migrate." + id, "migrate_status." + id}
}
func (s *sqlStore) routeMigration(ctx context.Context, scope, operation string, input protocol.SQLRequest) (any, error) {
	connection, err := s.engine.node.FindLeader(ctx)
	if err != nil {
		return nil, sqlError(503, "SQL leader is unavailable")
	}
	leader, err := connection.Leader(ctx)
	connection.Close()
	if err != nil || leader == nil {
		return nil, sqlError(503, "SQL leader is unavailable")
	}
	base, _, targeted := strings.Cut(operation, ".")
	if leader.ID != s.engine.node.ID() {
		if targeted {
			return nil, sqlError(503, "SQL leadership changed; retry migration status before applying")
		}
		data, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		reply, err := s.core.nc.RequestWithContext(ctx, protocol.SQLSubject(scope, base+"."+strconv.FormatUint(leader.ID, 10)), data)
		if err != nil {
			return nil, sqlError(503, "SQL migration leader is unavailable")
		}
		if code := reply.Header.Get("Nats-Service-Error-Code"); code != "" {
			number, _ := strconv.Atoi(code)
			return nil, sqlError(number, reply.Header.Get("Nats-Service-Error"))
		}
		return json.RawMessage(reply.Data), nil
	}
	return s.migrate(ctx, scope, input.Migrations, base == "migrate")
}

// Validate before forwarding SQL to dqlite's privileged replication transport.
// Database names come from the authenticated NATS subject, never from SQL input.
func validateRuntimeSQL(statement string) error {
	lexer := sqliteparser.NewSQLiteLexer(antlr.NewInputStream(statement))
	lexer.RemoveErrorListeners()
	first := ""
	ended := false
	for _, token := range lexer.GetAllTokens() {
		if token.GetChannel() != antlr.TokenDefaultChannel {
			continue
		}
		if token.GetTokenType() == sqliteparser.SQLiteLexerSCOL {
			ended = true
			continue
		}
		if ended {
			return protocol.BadRequest("SQL query accepts one statement")
		}
		word := strings.ToLower(strings.Trim(token.GetText(), "\"'`[]"))
		if first == "" {
			first = word
		}
		if strings.HasPrefix(word, "sqlite_") || strings.HasPrefix(word, "pragma_") || strings.HasPrefix(word, "keel_") {
			return sqlError(403, "SQL operation is reserved")
		}
		switch word {
		case "goose_db_version", "load_extension", "readfile", "writefile", "fsdir", "zipfile", "eval":
			return sqlError(403, "SQL operation is reserved or unsafe")
		}
		if token.GetTokenType() == sqliteparser.SQLiteLexerSTRING_LITERAL {
			continue
		}
		switch word {
		case "attach", "detach", "pragma", "vacuum", "begin", "commit", "rollback", "savepoint", "release", "create", "drop", "alter", "reindex", "analyze":
			return sqlError(403, "runtime SQL cannot change schemas or sessions")
		}
	}
	switch first {
	case "select", "with", "insert", "update", "delete", "replace", "explain":
		return nil
	}
	return sqlError(403, "runtime SQL accepts queries and data changes; use migrations for schema changes")
}
