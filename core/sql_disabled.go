//go:build (!keel_libsql && !keel_dqlite) || !cgo

package core

import "errors"

type sqlStore struct{}

func startSQL(_ *Core, options Options) (*sqlStore, error) {
	if options.SQL {
		return nil, errors.New("embedded SQL requires CGO_ENABLED=1 and a keel_libsql or keel_dqlite build")
	}
	return nil, nil
}
func (*sqlStore) close() {}
