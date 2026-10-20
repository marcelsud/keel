package project

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marcelsud/keel/core"
)

type DevOptions struct {
	Core            core.Options
	NoCore          bool
	WatchOnly       bool
	Nodes           int
	targetOverride  bool
	commandOverride bool
}

func fingerprint(root string, projects []localProject, targets []Target) ([32]byte, error) {
	files := map[string][]byte{}
	ignored := map[string]bool{}
	for _, target := range targets {
		ignored[target.Output] = true
	}
	read := func(name string) error {
		data, err := os.ReadFile(name)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err == nil {
			files[name] = data
		}
		return err
	}
	walk := func(directory string, allSource bool) error {
		return filepath.WalkDir(directory, func(name string, entry fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if name != directory && (ignored[name] || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "dist" || entry.Name() == "data") {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			ext := filepath.Ext(name)
			if ext == ".proto" || allSource && (ext == ".sql" || ext == ".go" || ext == ".ts" || ext == ".js" || ext == ".mjs" || ext == ".cjs" || ext == ".json" || entry.Name() == "go.mod" || entry.Name() == "go.sum" || entry.Name() == "bun.lock") {
				return read(name)
			}
			return nil
		})
	}
	if err := walk(root, true); err != nil {
		return [32]byte{}, err
	}
	for _, project := range projects {
		if err := walk(protoDir(project), false); err != nil {
			return [32]byte{}, err
		}
		for _, name := range []string{configName, jsonConfigName} {
			if err := read(filepath.Join(project.root, name)); err != nil {
				return [32]byte{}, err
			}
		}
	}
	hash := sha256.New()
	for _, name := range sortedKeys(files) {
		fmt.Fprintf(hash, "%d:%s:%d:", len(name), name, len(files[name]))
		hash.Write(files[name])
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

type runningCommand struct {
	command *exec.Cmd
	done    chan error
}

type lockedWriter struct {
	io.Writer
	mu sync.Mutex
}

func (writer *lockedWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.Writer.Write(data)
}

func startCommand(root string, command []string, environment []string, output io.Writer) (*runningCommand, error) {
	process := exec.Command(command[0], command[1:]...)
	process.Dir, process.Env, process.Stdout, process.Stderr = root, environment, output, output
	prepareProcess(process)
	if err := process.Start(); err != nil {
		return nil, err
	}
	running := &runningCommand{command: process, done: make(chan error, 1)}
	go func() { running.done <- process.Wait() }()
	return running, nil
}

func (running *runningCommand) stop() {
	if running == nil {
		return
	}
	stopProcess(running.command.Process, false)
	select {
	case <-running.done:
	case <-time.After(3 * time.Second):
		stopProcess(running.command.Process, true)
		<-running.done
	}
	stopProcess(running.command.Process, true)
}

// Dev regenerates contracts, starts the environment, and restarts the configured service.
func Dev(ctx context.Context, root string, config Config, options DevOptions, output io.Writer) error {
	if options.Nodes != 0 && options.Nodes != 1 && options.Nodes != 3 {
		return errors.New("local development supports --nodes 1 or --nodes 3")
	}
	if options.Nodes == 3 && (options.NoCore || options.WatchOnly || os.Getenv("KEEL_URL") != "") {
		return errors.New("--nodes 3 starts its own local cluster; remove --no-core, --watch-only, and KEEL_URL")
	}
	output = &lockedWriter{Writer: output}
	if !options.WatchOnly && len(config.Command) == 0 {
		return errors.New("set command in keel.toml or keel.json, pass keel dev -- <command> [args], or use --watch-only")
	}
	projects, targets, err := generate(ctx, root, config, output, true)
	if err != nil {
		return err
	}
	environment := os.Environ()
	var credentials core.Credentials
	var sqlURL = os.Getenv("KEEL_URL")
	if len(config.SQL) > 0 || options.Nodes == 3 {
		var path string
		credentials, path, err = ensureCredentials(root, config)
		if err != nil {
			return err
		}
		options.Core.Credentials = &credentials
		options.Core.SQL = len(config.SQL) > 0
		environment = append(environment, "KEEL_CREDENTIALS_FILE="+path, "KEEL_TOKEN=", "KEEL_USER=")
		if len(credentials.Services) == 1 {
			for service, secret := range credentials.Services {
				environment = append(environment, "KEEL_USER="+service, "KEEL_TOKEN="+secret)
			}
		}
	}
	if !options.WatchOnly && !options.NoCore && os.Getenv("KEEL_URL") == "" {
		var url string
		var close func()
		if options.Nodes == 3 {
			url, close, err = startLocalCluster(root, options.Core)
		} else {
			var server *core.Core
			server, err = core.Start(options.Core)
			if err == nil {
				url, close = server.URL(), server.Close
			}
		}
		if err != nil {
			return err
		}
		defer close()
		environment = append(environment, "KEEL_URL="+url)
		sqlURL = url
		fmt.Fprintf(output, "keel ready: %s\n", url)
	}
	current, err := fingerprint(root, projects, targets)
	if err != nil {
		return err
	}
	var running *runningCommand
	var done <-chan error
	start := func(command []string) error {
		if options.WatchOnly {
			return nil
		}
		if len(config.SQL) > 0 {
			if err := applySQL(ctx, root, config, sqlURL, credentials, output); err != nil {
				return err
			}
		}
		fmt.Fprintf(output, "running: %s\n", strings.Join(command, " "))
		var err error
		running, err = startCommand(root, command, environment, output)
		if err == nil {
			done = running.done
		}
		return err
	}
	if err := start(config.Command); err != nil {
		return err
	}
	defer func() { running.stop() }()
	fmt.Fprintln(output, "watching local sources and contracts; Ctrl-C to stop")
	// ponytail: poll local files; use filesystem notifications if large workspaces make scans expensive.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-done:
			fmt.Fprintf(output, "service exited (%v); waiting for changes\n", err)
			stopProcess(running.command.Process, true)
			running, done = nil, nil
		case <-ticker.C:
			next, err := fingerprint(root, projects, targets)
			if err != nil {
				return err
			}
			if next == current {
				continue
			}
			current = next
			updated, err := load(root)
			if err == nil {
				if options.targetOverride {
					updated.Targets = config.Targets
				}
				if options.commandOverride {
					updated.Command = config.Command
				}
				if !options.WatchOnly && len(updated.Command) == 0 {
					fmt.Fprintln(output, "set command in keel.toml or keel.json or pass keel dev -- <command> [args]")
					continue
				}
				var newProjects []localProject
				var newTargets []Target
				newProjects, newTargets, err = generate(ctx, root, updated, output, true)
				if err == nil {
					projects, targets, config = newProjects, newTargets, updated
					current, err = fingerprint(root, projects, targets)
				}
			}
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				fmt.Fprintf(output, "generation failed: %v\n", err)
				continue
			}
			running.stop()
			running, done = nil, nil
			if err := start(config.Command); err != nil {
				fmt.Fprintf(output, "service failed to start: %v\n", err)
			}
		}
	}
}
