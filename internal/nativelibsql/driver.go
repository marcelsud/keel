//go:build keel_libsql && !keel_dqlite && cgo

// Package nativelibsql adapts libSQL's embedded SQLite C ABI to database/sql.
// The upstream Go adapter coerces TEXT dates and cannot interrupt queries; this
// adapter preserves storage types and uses the engine's cancellation/authorizer.
package nativelibsql

/*
#include <stdlib.h>
#include <string.h>
// These declarations are the stable SQLite ABI exported by the native libSQL
// library linked by go-libsql. No system SQLite headers or library are required.
typedef struct sqlite3 sqlite3;
typedef struct sqlite3_stmt sqlite3_stmt;
int sqlite3_open_v2(const char*, sqlite3**, int, const char*);
int sqlite3_close_v2(sqlite3*);
const char *sqlite3_errmsg(sqlite3*);
int sqlite3_prepare_v2(sqlite3*, const char*, int, sqlite3_stmt**, const char**);
int sqlite3_step(sqlite3_stmt*);
int sqlite3_finalize(sqlite3_stmt*);
int sqlite3_bind_parameter_count(sqlite3_stmt*);
int sqlite3_bind_null(sqlite3_stmt*, int);
int sqlite3_bind_int64(sqlite3_stmt*, int, long long);
int sqlite3_bind_double(sqlite3_stmt*, int, double);
int sqlite3_bind_text(sqlite3_stmt*, int, const char*, int, void(*)(void*));
int sqlite3_bind_blob(sqlite3_stmt*, int, const void*, int, void(*)(void*));
int sqlite3_column_count(sqlite3_stmt*);
const char *sqlite3_column_name(sqlite3_stmt*, int);
const char *sqlite3_column_decltype(sqlite3_stmt*, int);
int sqlite3_column_type(sqlite3_stmt*, int);
long long sqlite3_column_int64(sqlite3_stmt*, int);
double sqlite3_column_double(sqlite3_stmt*, int);
const unsigned char *sqlite3_column_text(sqlite3_stmt*, int);
const void *sqlite3_column_blob(sqlite3_stmt*, int);
int sqlite3_column_bytes(sqlite3_stmt*, int);
long long sqlite3_changes64(sqlite3*);
long long sqlite3_last_insert_rowid(sqlite3*);
void sqlite3_interrupt(sqlite3*);
int sqlite3_busy_timeout(sqlite3*, int);
int sqlite3_limit(sqlite3*, int, int);
int sqlite3_stricmp(const char*,const char*);
int sqlite3_strnicmp(const char*,const char*,int);
int sqlite3_set_authorizer(sqlite3*, int(*)(void*,int,const char*,const char*,const char*,const char*), void*);
void sqlite3_progress_handler(sqlite3*, int, int(*)(void*), void*);
static int keel_text(sqlite3_stmt *s, int i, const char *v, int n) {
  return sqlite3_bind_text(s,i,v,n,(void(*)(void*))-1);
}
static int keel_blob(sqlite3_stmt *s, int i, const void *v, int n) {
  return sqlite3_bind_blob(s,i,v,n,(void(*)(void*))-1);
}
static int keel_authorize(void *p, int action, const char *a, const char *b, const char *db, const char *trigger) {
  // SQLITE_READ/INSERT/UPDATE/DELETE: protect the migration ledger and schema.
  if (action==20 || action==18 || action==23 || action==9) {
    if (a && (!sqlite3_stricmp(a,"goose_db_version") || !sqlite3_strnicmp(a,"sqlite_",7))) return 1;
    return 0;
  }
  if (action==21 || action==33) return 0; // SELECT, RECURSIVE
  if (action==31) { // FUNCTION
    if (b && (!sqlite3_stricmp(b,"load_extension") || !sqlite3_stricmp(b,"readfile") ||
        !sqlite3_stricmp(b,"writefile") || !sqlite3_stricmp(b,"fsdir") || !sqlite3_stricmp(b,"zipfile"))) return 1;
    return 0;
  }
  return 1; // Schema/session changes, ATTACH, PRAGMA, transaction control.
}
static int keel_budget(void *p) { return --(*(int*)p) <= 0; }
static int *keel_runtime(sqlite3 *db) {
  int *budget = malloc(sizeof(int));
  if (!budget) return NULL;
  *budget = 50000; // 50 million VM instructions per statement.
  sqlite3_set_authorizer(db, keel_authorize, NULL);
  sqlite3_progress_handler(db,1000,keel_budget,budget);
  return budget;
}
static void keel_reset(sqlite3 *db, int *budget) {
  sqlite3_set_authorizer(db,NULL,NULL);
  sqlite3_progress_handler(db,0,NULL,NULL);
  free(budget);
}
*/
import "C"

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unsafe"

	// Links libSQL's native engine on supported platforms, including its SQLite ABI.
	_ "github.com/tursodatabase/go-libsql"
)

type runtimeKey struct{}

func Runtime(ctx context.Context) context.Context { return context.WithValue(ctx, runtimeKey{}, true) }

func init() { sql.Register("keel-libsql", nativeDriver{}) }

type nativeDriver struct{}
type conn struct{ db *C.sqlite3 }
type statement struct {
	c     *conn
	query string
}
type transaction struct{ c *conn }
type result struct{ changed, inserted int64 }

func (r result) RowsAffected() (int64, error) { return r.changed, nil }
func (r result) LastInsertId() (int64, error) { return r.inserted, nil }

func (nativeDriver) Open(path string) (driver.Conn, error) {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	c := &conn{}
	// READWRITE | CREATE | FULLMUTEX: interrupt is called by another goroutine.
	if rc := C.sqlite3_open_v2(name, &c.db, 2|4|65536, nil); rc != 0 {
		err := c.err(rc)
		if c.db != nil {
			C.sqlite3_close_v2(c.db)
		}
		return nil, err
	}
	C.sqlite3_busy_timeout(c.db, 2000)
	C.sqlite3_limit(c.db, 0, 1024*1024) // SQLITE_LIMIT_LENGTH, including individual results.
	C.sqlite3_limit(c.db, 2, 256)       // SQLITE_LIMIT_COLUMN
	C.sqlite3_limit(c.db, 9, 256)       // SQLITE_LIMIT_VARIABLE_NUMBER
	if _, err := c.ExecContext(context.Background(), "PRAGMA foreign_keys=ON; PRAGMA synchronous=FULL", nil); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *conn) err(rc C.int) error {
	return fmt.Errorf("libSQL (%d): %s", int(rc), C.GoString(C.sqlite3_errmsg(c.db)))
}
func (c *conn) Close() error {
	if rc := C.sqlite3_close_v2(c.db); rc != 0 {
		return c.err(rc)
	}
	return nil
}
func (c *conn) Prepare(query string) (driver.Stmt, error) { return &statement{c, query}, nil }
func (c *conn) Begin() (driver.Tx, error)                 { return c.BeginTx(context.Background(), driver.TxOptions{}) }
func (c *conn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if options.ReadOnly || options.Isolation != 0 {
		return nil, errors.New("unsupported libSQL transaction options")
	}
	_, err := c.ExecContext(ctx, "BEGIN IMMEDIATE", nil)
	return transaction{c}, err
}
func (t transaction) Commit() error {
	_, err := t.c.ExecContext(context.Background(), "COMMIT", nil)
	return err
}
func (t transaction) Rollback() error {
	_, err := t.c.ExecContext(context.Background(), "ROLLBACK", nil)
	return err
}
func (s *statement) Close() error  { return nil }
func (s *statement) NumInput() int { return -1 }
func named(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, v := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return out
}
func (s *statement) Exec(args []driver.Value) (driver.Result, error) {
	return s.c.ExecContext(context.Background(), s.query, named(args))
}
func (s *statement) Query(args []driver.Value) (driver.Rows, error) {
	return s.c.QueryContext(context.Background(), s.query, named(args))
}

// watch must be stopped before a connection is returned to the pool or closed.
func (c *conn) watch(ctx context.Context) func() {
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { C.sqlite3_interrupt(c.db); close(finished) })
	return func() {
		if !stop() {
			<-finished
		}
	}
}

func (c *conn) prepare(query string, args []driver.NamedValue) (*C.sqlite3_stmt, string, error) {
	if strings.IndexByte(query, 0) >= 0 {
		return nil, "", errors.New("SQL cannot contain NUL")
	}
	source := C.CString(query)
	defer C.free(unsafe.Pointer(source))
	var stmt *C.sqlite3_stmt
	var tail *C.char
	if rc := C.sqlite3_prepare_v2(c.db, source, -1, &stmt, &tail); rc != 0 {
		return nil, "", c.err(rc)
	}
	rest := C.GoString(tail)
	if stmt == nil {
		return nil, rest, nil
	}
	if len(args) != int(C.sqlite3_bind_parameter_count(stmt)) {
		C.sqlite3_finalize(stmt)
		return nil, "", errors.New("SQL parameter count does not match placeholders")
	}
	for i, arg := range args {
		if arg.Name != "" {
			C.sqlite3_finalize(stmt)
			return nil, "", errors.New("use positional SQL parameters")
		}
		var rc C.int
		switch value := arg.Value.(type) {
		case nil:
			rc = C.sqlite3_bind_null(stmt, C.int(i+1))
		case int64:
			rc = C.sqlite3_bind_int64(stmt, C.int(i+1), C.longlong(value))
		case float64:
			rc = C.sqlite3_bind_double(stmt, C.int(i+1), C.double(value))
		case bool:
			var n C.longlong
			if value {
				n = 1
			}
			rc = C.sqlite3_bind_int64(stmt, C.int(i+1), n)
		case string:
			v := C.CString(value)
			rc = C.keel_text(stmt, C.int(i+1), v, C.int(len(value)))
			C.free(unsafe.Pointer(v))
		case []byte:
			v := C.CBytes(value)
			rc = C.keel_blob(stmt, C.int(i+1), v, C.int(len(value)))
			C.free(v)
		default:
			C.sqlite3_finalize(stmt)
			return nil, "", fmt.Errorf("unsupported SQL parameter %T", value)
		}
		if rc != 0 {
			C.sqlite3_finalize(stmt)
			return nil, "", c.err(rc)
		}
	}
	return stmt, rest, nil
}

func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	stop := c.watch(ctx)
	defer stop()
	for strings.TrimSpace(query) != "" {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		stmt, tail, err := c.prepare(query, args)
		if err != nil {
			return nil, err
		}
		query = tail
		if stmt == nil {
			continue
		}
		for {
			rc := C.sqlite3_step(stmt)
			if rc == 100 {
				continue
			}
			C.sqlite3_finalize(stmt)
			if rc != 101 {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, c.err(rc)
			}
			break
		}
	}
	return result{int64(C.sqlite3_changes64(c.db)), int64(C.sqlite3_last_insert_rowid(c.db))}, nil
}

type rows struct {
	c       *conn
	stmt    *C.sqlite3_stmt
	columns []string
	ctx     context.Context
	stop    func()
	budget  *C.int
}

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r := &rows{c: c, ctx: ctx, stop: c.watch(ctx)}
	if ctx.Value(runtimeKey{}) == true {
		r.budget = C.keel_runtime(c.db)
		if r.budget == nil {
			r.Close()
			return nil, errors.New("cannot allocate SQL execution budget")
		}
	}
	stmt, tail, err := c.prepare(query, args)
	if err != nil {
		r.Close()
		return nil, err
	}
	r.stmt = stmt
	// Preparing the remainder distinguishes comments/whitespace from a second statement.
	if strings.TrimSpace(tail) != "" {
		extra, _, err := c.prepare(tail, nil)
		if extra != nil {
			C.sqlite3_finalize(extra)
		}
		if err != nil || extra != nil {
			r.Close()
			return nil, errors.New("SQL query accepts one statement")
		}
	}
	if stmt != nil {
		for i := 0; i < int(C.sqlite3_column_count(stmt)); i++ {
			r.columns = append(r.columns, C.GoString(C.sqlite3_column_name(stmt, C.int(i))))
		}
	}
	return r, nil
}
func (r *rows) Columns() []string { return r.columns }
func (r *rows) Close() error {
	if r.stop == nil {
		return nil
	}
	r.stop()
	r.stop = nil
	if r.stmt != nil {
		C.sqlite3_finalize(r.stmt)
		r.stmt = nil
	}
	C.keel_reset(r.c.db, r.budget)
	return nil
}
func (r *rows) Next(values []driver.Value) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if r.stmt == nil {
		return io.EOF
	}
	rc := C.sqlite3_step(r.stmt)
	if rc == 101 {
		return io.EOF
	}
	if rc != 100 {
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
		return r.c.err(rc)
	}
	for i := range values {
		index := C.int(i)
		switch C.sqlite3_column_type(r.stmt, index) {
		case 1:
			values[i] = int64(C.sqlite3_column_int64(r.stmt, index))
		case 2:
			values[i] = float64(C.sqlite3_column_double(r.stmt, index))
		case 3:
			values[i] = C.GoStringN((*C.char)(unsafe.Pointer(C.sqlite3_column_text(r.stmt, index))), C.sqlite3_column_bytes(r.stmt, index))
			// Goose scans its declared TIMESTAMP ledger column into time.Time.
			// Runtime SQL always preserves TEXT exactly, regardless of its contents.
			if r.ctx.Value(runtimeKey{}) != true && strings.EqualFold(C.GoString(C.sqlite3_column_decltype(r.stmt, index)), "timestamp") {
				for _, format := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999"} {
					if parsed, err := time.Parse(format, values[i].(string)); err == nil {
						values[i] = parsed
						break
					}
				}
			}
		case 4:
			values[i] = C.GoBytes(C.sqlite3_column_blob(r.stmt, index), C.sqlite3_column_bytes(r.stmt, index))
		case 5:
			values[i] = nil
		}
	}
	return nil
}
