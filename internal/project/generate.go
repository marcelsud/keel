package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marcelsud/keel/internal/protocol"
	"github.com/marcelsud/keel/internal/pull"
	"github.com/marcelsud/keel/internal/toolchain"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

const manifestName = ".keel.generated.json"

type manifest struct {
	Language string   `json:"language"`
	SHA256   string   `json:"sha256"`
	Files    []string `json:"files"`
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sources(projects []localProject) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, project := range projects {
		root := protoDir(project)
		err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) && name == root {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(name, ".proto") {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("contract files must not be symlinks: %s", name)
			}
			relative, err := filepath.Rel(root, name)
			if err != nil {
				return err
			}
			key := filepath.ToSlash(relative)
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			if previous, exists := files[key]; exists && !bytes.Equal(previous, data) {
				return fmt.Errorf("conflicting local contracts define %s", key)
			}
			files[key] = data
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func compile(ctx context.Context, files map[string][]byte) (*descriptorpb.FileDescriptorSet, []byte, error) {
	compiler, err := toolchain.LookPath("buf")
	if err != nil {
		return nil, nil, errors.New("buf not found; reinstall the Keel release bundle or see the README source-build steps")
	}
	directory, err := os.MkdirTemp("", "keel-contracts-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(directory)
	for _, name := range sortedKeys(files) {
		if err := pull.WriteFile(directory, name, files[name]); err != nil {
			return nil, nil, err
		}
	}
	command := exec.CommandContext(ctx, compiler, "build", "--config", `{"version":"v2"}`, "--as-file-descriptor-set", "-o", "-")
	command.Dir = directory
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("contract compilation failed: %w\n%s", err, stderr.String())
	}
	set := new(descriptorpb.FileDescriptorSet)
	if err := proto.Unmarshal(data, set); err != nil {
		return nil, nil, err
	}
	if _, err := protodesc.NewFiles(set); err != nil {
		return nil, nil, err
	}
	return set, data, nil
}

// Generate compiles local contracts and all local dependencies without a server.
func Generate(ctx context.Context, root string, config Config, output io.Writer) ([]localProject, []Target, error) {
	return generate(ctx, root, config, output, false)
}

func generate(ctx context.Context, root string, config Config, output io.Writer, allowEmpty bool) ([]localProject, []Target, error) {
	projects, err := graph(root, config)
	if err != nil {
		return nil, nil, err
	}
	files, err := sources(projects)
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		if !allowEmpty {
			return nil, nil, errors.New("no contracts found; add .proto files under proto/ or use a local dependency with keel use")
		}
		fmt.Fprintln(output, "no protobuf contracts; skipping generation")
		return projects, nil, nil
	}
	targets, err := resolveTargets(root, config)
	if err != nil {
		return nil, nil, err
	}
	set, data, err := compile(ctx, files)
	if err != nil {
		return nil, nil, err
	}
	generated := make([]map[string]string, len(targets))
	for i, target := range targets {
		generated[i], err = pull.Bindings(ctx, set, pull.Options{
			Output: target.Output, Language: target.Language, GoPackagePrefix: target.GoPackagePrefix,
			GoPlugin: target.GoPlugin, ESPlugin: target.ESPlugin,
		})
		if err != nil {
			return nil, nil, err
		}
		if err := validateOutput(target, generated[i]); err != nil {
			return nil, nil, err
		}
	}
	for i, target := range targets {
		if err := writeOutput(target, generated[i], protocol.Digest(data)); err != nil {
			return nil, nil, err
		}
		fmt.Fprintf(output, "generated %s bindings in %s\n", target.Language, target.Output)
	}
	return projects, targets, nil
}

func previousFiles(target Target) (manifest, error) {
	var previous manifest
	data, err := os.ReadFile(filepath.Join(target.Output, manifestName))
	if errors.Is(err, os.ErrNotExist) {
		return previous, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &previous)
	}
	if err != nil {
		return previous, err
	}
	if previous.Language != target.Language {
		return previous, errors.New("use different output directories for different generated languages")
	}
	for _, name := range previous.Files {
		if !safeRelative(name) {
			return previous, fmt.Errorf("unsafe generated manifest path %q", name)
		}
	}
	return previous, nil
}

func safeRelative(name string) bool {
	return name != "" && name != "." && name != ".." && !path.IsAbs(name) && path.Clean(name) == name && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\:\x00")
}

func validateOutput(target Target, files map[string]string) error {
	previous, err := previousFiles(target)
	if err != nil {
		return err
	}
	owned := map[string]bool{}
	for _, name := range previous.Files {
		owned[name] = true
	}
	for name, content := range files {
		if !safeRelative(name) {
			return fmt.Errorf("unsafe generated path %q", name)
		}
		filename := filepath.Join(target.Output, filepath.FromSlash(name))
		for directory := filepath.Dir(filename); directory != filepath.Dir(directory); directory = filepath.Dir(directory) {
			if info, err := os.Lstat(directory); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
				return fmt.Errorf("generated path crosses a symlink or non-directory: %s", directory)
			}
		}
		current, err := os.ReadFile(filename)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		generated := bytes.HasPrefix(current, []byte("// Code generated by ")) || bytes.HasPrefix(current, []byte("// @generated by "))
		if !owned[name] && !generated && string(current) != content {
			return fmt.Errorf("refusing to overwrite non-generated file %s", filename)
		}
	}
	return nil
}

func writeOutput(target Target, files map[string]string, digest string) error {
	previous, err := previousFiles(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(target.Output, 0755); err != nil {
		return err
	}
	for _, name := range sortedKeys(files) {
		filename := filepath.Join(target.Output, filepath.FromSlash(name))
		if existing, err := os.ReadFile(filename); err == nil && string(existing) == files[name] {
			continue
		}
		if err := pull.WriteFile(target.Output, name, []byte(files[name])); err != nil {
			return err
		}
	}
	for _, name := range previous.Files {
		if _, exists := files[name]; exists {
			continue
		}
		filename := filepath.Join(target.Output, filepath.FromSlash(name))
		resolved, err := filepath.EvalSymlinks(filename)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(target.Output, resolved)
		if err != nil || !safeRelative(filepath.ToSlash(relative)) {
			return fmt.Errorf("obsolete generated file escapes output: %s", name)
		}
		if err := os.Remove(filename); err != nil {
			return err
		}
	}
	encoded, err := json.MarshalIndent(manifest{Language: target.Language, SHA256: digest, Files: sortedKeys(files)}, "", "  ")
	if err != nil {
		return err
	}
	return pull.WriteFile(target.Output, manifestName, append(encoded, '\n'))
}
