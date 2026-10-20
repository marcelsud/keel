//go:build !cgo || (!keel_dqlite && !((linux && (amd64 || arm64)) || (darwin && arm64)))

package core

import "errors"

type sqlStore struct{}

func startSQL(_ *Core, options Options) (*sqlStore, error) {
	if options.SQL {
		return nil, errors.New("embedded SQL requires CGO_ENABLED=1 on Linux amd64/arm64 or macOS arm64; clustered SQL requires a keel_dqlite build")
	}
	return nil, nil
}
func (*sqlStore) close() {}
