package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/marcelsud/keel/internal/protocol"
)

type SQLResult = protocol.SQLResult
type Migration = protocol.Migration

// SQLCommitUnknownError means COMMIT was requested but its outcome cannot be confirmed.
type SQLCommitUnknownError struct{ Cause error }

func (e *SQLCommitUnknownError) Error() string {
	return "SQL commit outcome unknown; reconcile using the operation's idempotency key: " + e.Cause.Error()
}
func (e *SQLCommitUnknownError) Unwrap() error { return e.Cause }

type SQL struct {
	app   *App
	scope string
}
type SQLTx struct {
	app    *App
	handle protocol.SQLTransaction
	mu     sync.Mutex
	closed bool
}

func sqlInput(statement string, params []any) (protocol.SQLRequest, error) {
	input := protocol.SQLRequest{Statement: statement}
	for _, param := range params {
		data, err := json.Marshal(param)
		if err != nil {
			return input, err
		}
		input.Params = append(input.Params, data)
	}
	return input, nil
}

func (s *SQL) Query(ctx context.Context, statement string, params ...any) (SQLResult, error) {
	input, err := sqlInput(statement, params)
	if err != nil {
		return SQLResult{}, err
	}
	var result SQLResult
	err = s.app.request(ctx, protocol.SQLSubject(s.scope, "query"), input, &result)
	return result, err
}

func (t *SQLTx) Query(ctx context.Context, statement string, params ...any) (SQLResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return SQLResult{}, errors.New("SQL transaction is closed")
	}
	input, err := sqlInput(statement, params)
	if err != nil {
		t.closed = true
		return SQLResult{}, err
	}
	input.Transaction = t.handle.ID
	var result SQLResult
	err = t.app.request(ctx, t.handle.Subject+".query", input, &result)
	if err != nil {
		t.closed = true
	}
	return result, err
}

func (s *SQL) Transaction(ctx context.Context, callback func(*SQLTx) error) (err error) {
	if callback == nil {
		return errors.New("SQL transaction callback is required")
	}
	var handle protocol.SQLTransaction
	if err = s.app.request(ctx, protocol.SQLSubject(s.scope, "begin"), protocol.SQLRequest{}, &handle); err != nil {
		return err
	}
	prefix := protocol.SQLSubject(s.scope, "tx.")
	if !strings.HasPrefix(handle.Subject, prefix) || !protocol.ValidName(strings.TrimPrefix(handle.Subject, prefix)) || handle.ID == "" {
		return errors.New("invalid SQL transaction handle")
	}
	tx := &SQLTx{app: s.app, handle: handle}
	committed := false
	defer func() {
		tx.mu.Lock()
		tx.closed = true
		tx.mu.Unlock()
		if !committed {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = s.app.request(cleanup, handle.Subject+".rollback", protocol.SQLRequest{Transaction: handle.ID}, nil)
		}
	}()
	if err = callback(tx); err != nil {
		return err
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return errors.New("SQL transaction failed before commit")
	}
	tx.closed = true
	if err = s.app.request(ctx, handle.Subject+".commit", protocol.SQLRequest{Transaction: handle.ID}, nil); err != nil {
		var public *Error
		if errors.As(err, &public) && (public.Code == 409 || public.Code == 410) {
			return err
		}
		return &SQLCommitUnknownError{Cause: err}
	}
	committed = true
	return nil
}

// Migrate applies service-owned SQL files through the administrator's NATS identity.
func (s *SQL) Migrate(ctx context.Context, service string, migrations []Migration) error {
	if !protocol.ValidName(service) {
		return fmt.Errorf("invalid SQL service %q", service)
	}
	return s.app.request(ctx, protocol.SQLSubject(service, "migrate"), protocol.SQLRequest{Migrations: migrations}, nil)
}

func (s *SQL) MigrationStatus(ctx context.Context, service string, migrations []Migration) (any, error) {
	if !protocol.ValidName(service) {
		return nil, errors.New("invalid SQL service")
	}
	var result any
	err := s.app.request(ctx, protocol.SQLSubject(service, "migrate_status"), protocol.SQLRequest{Migrations: migrations}, &result)
	return result, err
}
