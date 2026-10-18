// Package toolchain locates tools shipped alongside the CLI.
package toolchain

import (
	"os"
	"os/exec"
	"path/filepath"
)

// LookPath prefers bundled tools and falls back to the user's PATH.
func LookPath(name string) (string, error) {
	if filepath.Base(name) == name {
		if executable, err := os.Executable(); err == nil {
			if resolved, err := filepath.EvalSymlinks(executable); err == nil {
				executable = resolved
			}
			if tool, err := exec.LookPath(filepath.Join(filepath.Dir(executable), name)); err == nil {
				return tool, nil
			}
		}
	}
	return exec.LookPath(name)
}
