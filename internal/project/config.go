// Package project implements local contract generation and development commands.
package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/marcelsud/keel/internal/toolchain"
)

const configName = "keel.json"

type Target struct {
	Language        string `json:"language"`
	Output          string `json:"out"`
	GoPackagePrefix string `json:"goPackagePrefix,omitempty"`
	GoPlugin        string `json:"goPlugin,omitempty"`
	ESPlugin        string `json:"esPlugin,omitempty"`
}

type Config struct {
	Proto        string            `json:"proto,omitempty"`
	Targets      []Target          `json:"targets,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
	Command      []string          `json:"command,omitempty"`
}

type localProject struct {
	root   string
	config Config
}

func load(root string) (Config, error) {
	var config Config
	file, err := os.Open(filepath.Join(root, configName))
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("%s: %w", configName, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return config, fmt.Errorf("%s must contain one JSON object", configName)
	}
	return config, nil
}

func absolute(root, name string) string {
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	return filepath.Join(root, name)
}

func protoDir(project localProject) string {
	name := project.config.Proto
	if name == "" {
		name = "proto"
	}
	return absolute(project.root, name)
}

func graph(root string, config Config) ([]localProject, error) {
	var projects []localProject
	seen, visiting := map[string]bool{}, map[string]bool{}
	var visit func(string, Config) error
	visit = func(directory string, current Config) error {
		real, err := filepath.EvalSymlinks(directory)
		if err != nil {
			return fmt.Errorf("local dependency %s: %w", directory, err)
		}
		if visiting[real] {
			return fmt.Errorf("local dependency cycle at %s", directory)
		}
		if seen[real] {
			return nil
		}
		visiting[real] = true
		projects = append(projects, localProject{root: real, config: current})
		for _, name := range sortedKeys(current.Dependencies) {
			source := current.Dependencies[name]
			if strings.TrimSpace(name) == "" || strings.TrimSpace(source) == "" || strings.Contains(source, "://") || strings.HasPrefix(source, "github:") {
				return fmt.Errorf("dependency %q must name a local project directory", name)
			}
			dependency := absolute(real, source)
			child, err := load(dependency)
			if err != nil {
				return err
			}
			if err := visit(dependency, child); err != nil {
				return err
			}
		}
		visiting[real] = false
		seen[real] = true
		return nil
	}
	err := visit(root, config)
	return projects, err
}

func resolveTargets(root string, config Config) ([]Target, error) {
	targets := append([]Target(nil), config.Targets...)
	if len(targets) == 0 {
		_, goErr := os.Stat(filepath.Join(root, "go.mod"))
		_, tsErr := os.Stat(filepath.Join(root, "package.json"))
		switch {
		case goErr == nil && tsErr == nil:
			return nil, errors.New("project contains go.mod and package.json; declare targets in keel.json")
		case goErr == nil:
			targets = []Target{{Language: "go", Output: "generated"}}
		case tsErr == nil:
			targets = []Target{{Language: "typescript", Output: "generated"}}
		default:
			return nil, errors.New("add go.mod, package.json, or generation targets in keel.json")
		}
	}
	seen := map[string]bool{}
	for i := range targets {
		target := &targets[i]
		if target.Language != "go" && target.Language != "typescript" {
			return nil, errors.New("target language must be go or typescript")
		}
		if target.Output == "" {
			target.Output = "generated"
		}
		target.Output = absolute(root, target.Output)
		if seen[target.Output] {
			return nil, errors.New("generation targets must use different output directories")
		}
		seen[target.Output] = true
		if target.Language == "go" && target.GoPackagePrefix == "" {
			module, err := os.ReadFile(filepath.Join(root, "go.mod"))
			if err != nil {
				return nil, fmt.Errorf("set goPackagePrefix or add go.mod: %w", err)
			}
			for _, line := range strings.Split(string(module), "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == "module" {
					relative, err := filepath.Rel(root, target.Output)
					if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
						return nil, errors.New("output outside the Go module requires goPackagePrefix")
					}
					target.GoPackagePrefix = strings.Trim(fields[1], "\"")
					if relative != "." {
						target.GoPackagePrefix += "/" + filepath.ToSlash(relative)
					}
					break
				}
			}
		}
		plugin, configured := "protoc-gen-go", target.GoPlugin
		if target.Language == "typescript" {
			plugin, configured = "protoc-gen-es", target.ESPlugin
		}
		resolved, err := resolvePlugin(root, target.Output, plugin, configured)
		if err != nil {
			return nil, err
		}
		if target.Language == "go" {
			target.GoPlugin = resolved
		} else {
			target.ESPlugin = resolved
		}
	}
	return targets, nil
}

func resolvePlugin(root, output, name, configured string) (string, error) {
	if configured != "" {
		if strings.ContainsAny(configured, "/\\") {
			return absolute(root, configured), nil
		}
		name = configured
	}
	for directory := output; ; directory = filepath.Dir(directory) {
		candidate := filepath.Join(directory, "node_modules", ".bin", name)
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved, nil
		}
		if directory == filepath.Dir(directory) {
			break
		}
	}
	if resolved, err := toolchain.LookPath(name); err == nil {
		return resolved, nil
	}
	return "", fmt.Errorf("%s not found; install the generator described in the README or configure its path in keel.json", name)
}
