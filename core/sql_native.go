//go:build cgo && (keel_dqlite || (linux && (amd64 || arm64)) || (darwin && arm64))

package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antlr4-go/antlr/v4"
	"github.com/libsql/sqlite-antlr4-parser/sqliteparser"
	"github.com/marcelsud/keel/internal/protocol"
	"github.com/nats-io/nats.go/micro"
	"github.com/pressly/goose/v3"
)

const sqlTTL = 30 * time.Second

type serviceDB struct {
	db          *sql.DB
	migrationDB *sql.DB
	migrations  sync.Mutex
}
type sqlLease struct {
	mu       sync.Mutex
	conn     *sql.Conn
	scope    string
	done     bool
	timer    *time.Timer
	deadline time.Time
}
type sqlStore struct {
	core             *Core
	directory, owner string
	databases        map[string]*serviceDB
	mu               sync.Mutex
	leases           map[string]*sqlLease
	slots            chan struct{}
	ttl              time.Duration
	engine           *sqlEngine
}

func sqlError(code int, message string) error { return &protocol.Error{Code: code, Message: message} }
func randomID() string {
	var id [24]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}

func startSQL(c *Core, options Options) (*sqlStore, error) {
	if !options.SQL {
		return nil, nil
	}
	if options.Credentials == nil || len(options.Credentials.Services) == 0 {
		return nil, errors.New("SQL requires per-service credentials")
	}
	s := &sqlStore{core: c, directory: filepath.Join(options.DataDir, "sql"), owner: randomID(), databases: map[string]*serviceDB{}, leases: map[string]*sqlLease{}, slots: make(chan struct{}, 64)}
	s.ttl = options.SQLTransactionTimeout
	if s.ttl == 0 {
		s.ttl = sqlTTL
	}
	if s.ttl < 100*time.Millisecond || s.ttl > 2*time.Minute {
		return nil, errors.New("SQL transaction timeout must be between 100ms and 2m")
	}
	success := false
	defer func() {
		if !success {
			s.close()
		}
	}()
	if err := s.openDatabases(options); err != nil {
		return nil, err
	}
	operations := []string{"query", "begin", "migrate", "migrate_status", "tx." + s.owner + ".query", "tx." + s.owner + ".commit", "tx." + s.owner + ".rollback"}
	operations = append(operations, s.migrationEndpoints()...)
	for _, operation := range operations {
		op := operation
		endpoint := strings.ReplaceAll(op, ".", "_")
		if err := c.service.AddEndpoint("sql_"+endpoint, micro.HandlerFunc(func(r micro.Request) { s.handle(op, r) }), micro.WithEndpointSubject(protocol.SQLSubject("*", op))); err != nil {
			return nil, err
		}
	}
	success = true
	return s, nil
}

func (s *sqlStore) handle(operation string, r micro.Request) {
	s.core.gate.Lock()
	if s.core.closing {
		s.core.gate.Unlock()
		respond(r, nil, sqlError(503, "core is stopping"))
		return
	}
	select {
	case s.slots <- struct{}{}:
	default:
		s.core.gate.Unlock()
		respond(r, nil, sqlError(503, "SQL request limit reached"))
		return
	}
	s.core.active.Add(1)
	s.core.gate.Unlock()
	go func() {
		defer s.core.active.Done()
		defer func() { <-s.slots }()
		parts := strings.Split(r.Subject(), ".")
		scope := parts[3]
		var input protocol.SQLRequest
		if err := json.Unmarshal(r.Data(), &input); err != nil {
			respond(r, nil, protocol.BadRequest("invalid SQL request"))
			return
		}
		result, err := s.execute(scope, operation, input)
		respond(r, result, err)
	}()
}

func (s *sqlStore) execute(scope, operation string, input protocol.SQLRequest) (any, error) {
	database := s.databases[scope]
	if database == nil {
		return nil, sqlError(403, "SQL service is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if operation == "migrate" || operation == "migrate_status" || strings.HasPrefix(operation, "migrate.") || strings.HasPrefix(operation, "migrate_status.") {
		return s.routeMigration(ctx, scope, operation, input)
	}
	switch operation {
	case "begin":
		conn, err := database.db.Conn(ctx)
		if err != nil {
			return nil, sqlError(503, "SQL connection is busy")
		}
		if err = beginSQL(ctx, conn); err != nil {
			discard(conn)
			return nil, sqlError(409, err.Error())
		}
		id := randomID()
		lease := &sqlLease{conn: conn, scope: scope, deadline: time.Now().Add(s.ttl)}
		s.mu.Lock()
		s.leases[id] = lease
		lease.timer = time.AfterFunc(s.ttl, func() { _ = s.finish(id, lease, false) })
		s.mu.Unlock()
		return protocol.SQLTransaction{ID: id, Subject: protocol.SQLSubject(scope, "tx."+s.owner), ExpiresAt: lease.deadline.UnixMilli()}, nil
	case "query":
		if err := checkSQL(input.Statement); err != nil {
			return nil, err
		}
		conn, err := database.db.Conn(ctx)
		if err != nil {
			return nil, sqlError(503, "SQL connection is busy")
		}
		defer conn.Close()
		if err = beginSQL(ctx, conn); err != nil {
			discard(conn)
			return nil, sqlError(409, err.Error())
		}
		result, err := runSQL(ctx, conn, input)
		if err != nil {
			if _, rollback := conn.ExecContext(context.Background(), "ROLLBACK"); rollback != nil {
				discard(conn)
			}
			return nil, err
		}
		if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
			discard(conn)
			return nil, sqlError(500, "SQL commit outcome unknown")
		}
		return result, nil
	}
	s.mu.Lock()
	lease := s.leases[input.Transaction]
	s.mu.Unlock()
	if lease == nil || lease.scope != scope {
		return nil, sqlError(410, "SQL transaction is closed or expired")
	}
	if strings.HasSuffix(operation, ".commit") {
		return nil, s.finish(input.Transaction, lease, true)
	}
	if strings.HasSuffix(operation, ".rollback") {
		return nil, s.finish(input.Transaction, lease, false)
	}
	lease.mu.Lock()
	if lease.done || time.Now().After(lease.deadline) {
		lease.mu.Unlock()
		_ = s.finish(input.Transaction, lease, false)
		return nil, sqlError(410, "SQL transaction is closed or expired")
	}
	err := checkSQL(input.Statement)
	var result protocol.SQLResult
	if err == nil {
		result, err = runSQL(ctx, lease.conn, input)
	}
	lease.mu.Unlock()
	if err != nil {
		_ = s.finish(input.Transaction, lease, false)
	}
	return result, err
}

func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

func beginSQL(ctx context.Context, conn *sql.Conn) error {
	for {
		_, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE")
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "database is locked") {
			return err
		}
		// Only BEGIN is retried, before application SQL has run.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (s *sqlStore) finish(id string, lease *sqlLease, commit bool) error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.done {
		return sqlError(410, "SQL transaction is closed or expired")
	}
	lease.done = true
	lease.timer.Stop()
	s.mu.Lock()
	delete(s.leases, id)
	s.mu.Unlock()
	command := "ROLLBACK"
	expired := commit && time.Now().After(lease.deadline)
	if commit && !expired {
		command = "COMMIT"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := lease.conn.ExecContext(ctx, command)
	if err != nil {
		discard(lease.conn)
		return sqlError(500, "SQL "+strings.ToLower(command)+" failed; outcome may be unknown")
	}
	err = lease.conn.Close()
	if expired {
		return sqlError(410, "SQL transaction expired before commit")
	}
	return err
}

func (s *sqlStore) close() {
	s.mu.Lock()
	leases := make(map[string]*sqlLease, len(s.leases))
	for id, lease := range s.leases {
		leases[id] = lease
	}
	s.mu.Unlock()
	for id, lease := range leases {
		_ = s.finish(id, lease, false)
	}
	for _, database := range s.databases {
		_ = database.db.Close()
		if database.migrationDB != nil {
			_ = database.migrationDB.Close()
		}
	}
	if s.engine != nil {
		s.engine.close()
	}
}

func runSQL(ctx context.Context, conn *sql.Conn, input protocol.SQLRequest) (protocol.SQLResult, error) {
	ctx = sqlRuntime(ctx)
	result := protocol.SQLResult{Rows: []map[string]any{}}
	if len(input.Params) > 256 {
		return result, protocol.BadRequest("SQL accepts at most 256 parameters")
	}
	params := make([]any, 0, len(input.Params))
	for _, raw := range input.Params {
		var value any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return result, protocol.BadRequest("invalid SQL parameter")
		}
		switch typed := value.(type) {
		case nil, string, bool:
		case json.Number:
			if integer, err := typed.Int64(); err == nil {
				value = integer
			} else if !strings.ContainsAny(string(typed), ".eE") {
				return result, protocol.BadRequest("SQL integer exceeds signed 64-bit range")
			} else if number, err := typed.Float64(); err == nil && !math.IsInf(number, 0) {
				value = number
			} else {
				return result, protocol.BadRequest("invalid SQL number")
			}
		default:
			return result, protocol.BadRequest("SQL parameters must be null, strings, booleans, or numbers")
		}
		if err := validateSQLParameter(value); err != nil {
			return result, err
		}
		params = append(params, value)
	}
	rows, err := conn.QueryContext(ctx, input.Statement, params...)
	if err != nil {
		return result, sqlError(409, err.Error())
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return result, err
	}
	size := 2
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return result, err
		}
		row := make(map[string]any, len(columns))
		for i, name := range columns {
			value := values[i]
			if integer, ok := value.(int64); ok && (integer > 9007199254740991 || integer < -9007199254740991) {
				value = strconv.FormatInt(integer, 10)
			}
			if blob, ok := value.([]byte); ok {
				value = map[string]any{"base64": base64.StdEncoding.EncodeToString(blob)}
			}
			row[name] = value
		}
		result.Rows = append(result.Rows, row)
		encoded, err := json.Marshal(row)
		if err != nil {
			return result, err
		}
		size += len(encoded) + 1
		if len(result.Rows) > 1000 || size > 512*1024 {
			return protocol.SQLResult{}, sqlError(413, "SQL result exceeds 1000 rows / 512 KiB")
		}
	}
	if err := rows.Err(); err != nil {
		return result, sqlError(409, err.Error())
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	result.RowCount = int64(len(result.Rows))
	if len(columns) == 0 {
		if err := conn.QueryRowContext(ctx, "SELECT changes()").Scan(&result.RowCount); err != nil {
			return result, err
		}
	}
	return result, nil
}

// The native engine authorizer enforces runtime SQL permissions during preparation.
func checkSQL(statement string) error {
	if len(statement) == 0 || len(statement) > 64*1024 {
		return protocol.BadRequest("SQL statement must contain 1–65536 bytes")
	}
	return validateRuntimeSQL(statement)
}

var migrationName = regexp.MustCompile(`^[0-9]+_[a-zA-Z0-9_-]+\.sql$`)

// Newly created migrations remain drafts until their Up section contains SQL.
// This prevents the development watcher from recording an empty file as applied.
func migrationHasSQL(source string) bool {
	var up strings.Builder
	for line := range strings.SplitSeq(source, "\n") {
		if strings.TrimSpace(line) == "-- +goose Down" {
			break
		}
		up.WriteString(line)
		up.WriteByte('\n')
	}
	lexer := sqliteparser.NewSQLiteLexer(antlr.NewInputStream(up.String()))
	lexer.RemoveErrorListeners()
	for _, token := range lexer.GetAllTokens() {
		if token.GetChannel() == antlr.TokenDefaultChannel && token.GetTokenType() != sqliteparser.SQLiteLexerSCOL {
			return true
		}
	}
	return false
}

func (s *sqlStore) migrate(ctx context.Context, scope string, files []protocol.Migration, apply bool) (any, error) {
	database := s.databases[scope]
	database.migrations.Lock()
	defer database.migrations.Unlock()
	if len(files) > 100 {
		return nil, protocol.BadRequest("at most 100 migrations per request")
	}
	directory, err := os.MkdirTemp("", "keel-sql-migrations-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	names := map[string]bool{}
	for _, file := range files {
		if !migrationName.MatchString(file.Name) || names[file.Name] || len(file.SQL) > 256*1024 {
			return nil, protocol.BadRequest("invalid SQL migration file")
		}
		if apply && !migrationHasSQL(file.SQL) {
			return nil, protocol.BadRequest("migration " + file.Name + " is a draft; add SQL to its Up section before applying")
		}
		names[file.Name] = true
		if err := os.WriteFile(filepath.Join(directory, file.Name), []byte(file.SQL), 0600); err != nil {
			return nil, err
		}
	}
	migrationDB := database.db
	if database.migrationDB != nil {
		migrationDB = database.migrationDB
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, migrationDB, os.DirFS(directory), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return nil, protocol.BadRequest(err.Error())
	}
	if apply {
		_, err = provider.Up(ctx)
		if err != nil {
			return nil, sqlError(409, err.Error())
		}
	}
	return provider.Status(ctx)
}
