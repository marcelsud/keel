// Package pull downloads a contract and generates client code using local plugins.
package pull

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marcelsud/keel/internal/protocol"
	"github.com/marcelsud/keel/internal/toolchain"
	"github.com/marcelsud/keel/sdk"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"
)

type Options struct {
	Output, Language, GoPackagePrefix, GoPlugin, ESPlugin string
	Update                                                bool
}

type lock struct {
	Service     string   `json:"service"`
	SHA256      string   `json:"sha256"`
	State       string   `json:"state"`
	Replacement string   `json:"replacement,omitempty"`
	Language    string   `json:"language"`
	Files       []string `json:"files"`
}

func Run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: keel pull <protobuf.service.Name> --lang typescript|go|both --out directory")
	}
	service := ""
	if !strings.HasPrefix(args[0], "-") {
		service, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("keel pull", flag.ContinueOnError)
	flags.SetOutput(output)
	var options Options
	var url string
	flags.StringVar(&url, "url", os.Getenv("KEEL_URL"), "NATS URL")
	flags.StringVar(&options.Output, "out", "./generated", "generated code directory")
	flags.StringVar(&options.Language, "lang", "typescript", "typescript, go, or both")
	flags.StringVar(&options.GoPackagePrefix, "go-package-prefix", "", "Go import path corresponding to the generated Go directory")
	flags.StringVar(&options.GoPlugin, "go-plugin", "protoc-gen-go", "local Go protobuf plugin")
	flags.StringVar(&options.ESPlugin, "es-plugin", "protoc-gen-es", "local TypeScript protobuf plugin")
	flags.BoolVar(&options.Update, "update", false, "accept a changed contract hash")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if service == "" {
		return errors.New("usage: keel pull <protobuf.service.Name> [options]")
	}
	if err := validateOptions(options); err != nil {
		return err
	}
	app, err := sdk.Connect(sdk.Options{URL: url, Name: "keel-pull", Token: os.Getenv("KEEL_TOKEN")})
	if err != nil {
		return err
	}
	defer app.Stop()
	schema, err := app.FetchContract(ctx, service)
	if err != nil {
		return err
	}
	if schema.State != sdk.Active {
		fmt.Fprintf(output, "warning: %s is %s; replacement: %s\n", schema.Service, schema.State, schema.Replacement)
	}
	if err := Generate(ctx, schema, options); err != nil {
		return err
	}
	fmt.Fprintf(output, "generated %s bindings for %s (%s) in %s\n", options.Language, schema.Service, schema.SHA256, options.Output)
	return nil
}

func validateOptions(options Options) error {
	if options.Output == "" {
		return errors.New("output directory is required")
	}
	if options.Language != "typescript" && options.Language != "go" && options.Language != "both" {
		return errors.New("language must be typescript, go, or both")
	}
	if options.Language != "typescript" {
		if options.GoPackagePrefix == "" || !safeName(options.GoPackagePrefix) || strings.ContainsAny(options.GoPackagePrefix, ",;= \t\r\n") {
			return errors.New("Go generation requires --go-package-prefix with the generated directory's Go import path")
		}
	}
	return nil
}

// Generate writes nothing until every plugin has succeeded and every output path is checked.
func Generate(ctx context.Context, schema sdk.Schema, options Options) error {
	if err := validateOptions(options); err != nil {
		return err
	}
	if schema.SHA256 != protocol.Digest(schema.DescriptorSet) {
		return errors.New("contract checksum mismatch")
	}
	_, set, err := protocol.Descriptor(schema.DescriptorSet, schema.Service)
	if err != nil {
		return err
	}
	lockName := schema.Service + ".lock.json"
	entries, err := os.ReadDir(options.Output)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".lock.json") && entry.Name() != lockName {
			return errors.New("output directory contains another contract; use a separate directory per contract")
		}
	}
	previous, err := os.ReadFile(filepath.Join(options.Output, lockName))
	var pinned lock
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if err = json.Unmarshal(previous, &pinned); err != nil {
			return fmt.Errorf("invalid contract lock: %w", err)
		}
		if pinned.Service != schema.Service {
			return errors.New("contract lock identity mismatch")
		}
		if pinned.SHA256 != schema.SHA256 && !options.Update {
			return errors.New("contract changed; review it and pass --update to regenerate")
		}
		if pinned.Language != options.Language {
			return errors.New("use a separate output directory when changing the generated language")
		}
		for _, name := range pinned.Files {
			if !safeName(name) {
				return fmt.Errorf("unsafe locked file path %q", name)
			}
		}
	}
	outputs, err := Bindings(ctx, set, options)
	if err != nil {
		return err
	}
	manifest := lock{Service: schema.Service, SHA256: schema.SHA256, State: schema.State, Replacement: schema.Replacement, Language: options.Language}
	for name := range outputs {
		manifest.Files = append(manifest.Files, name)
	}
	sort.Strings(manifest.Files)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(options.Output, 0755); err != nil {
		return err
	}
	for _, name := range manifest.Files {
		if err = writeFile(options.Output, name, []byte(outputs[name])); err != nil {
			return err
		}
	}
	if err = writeFile(options.Output, schema.Service+".binpb", schema.DescriptorSet); err != nil {
		return err
	}
	resolvedRoot, err := filepath.EvalSymlinks(options.Output)
	if err != nil {
		return err
	}
	for _, name := range pinned.Files {
		if _, exists := outputs[name]; exists {
			continue
		}
		filename := filepath.Join(options.Output, filepath.FromSlash(name))
		resolved, err := filepath.EvalSymlinks(filename)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(resolvedRoot, resolved)
		if err != nil || !safeName(filepath.ToSlash(relative)) {
			return fmt.Errorf("obsolete generated file escapes output directory: %s", name)
		}
		if err = os.Remove(filename); err != nil {
			return err
		}
	}
	return writeFile(options.Output, lockName, append(encoded, '\n'))
}

func runPlugin(ctx context.Context, executable string, request *pluginpb.CodeGeneratorRequest) (*pluginpb.CodeGeneratorResponse, error) {
	data, err := proto.Marshal(request)
	if err != nil {
		return nil, err
	}
	resolved, err := toolchain.LookPath(executable)
	if err != nil {
		return nil, fmt.Errorf("generator %s not found: %w", executable, err)
	}
	command := exec.CommandContext(ctx, resolved)
	command.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err = command.Output()
	if err != nil {
		return nil, fmt.Errorf("local plugin %s failed: %w: %s", executable, err, stderr.String())
	}
	response := new(pluginpb.CodeGeneratorResponse)
	if err = proto.Unmarshal(data, response); err != nil {
		return nil, fmt.Errorf("invalid plugin response: %w", err)
	}
	return response, nil
}

func safeName(name string) bool {
	return name != "" && name != "." && !path.IsAbs(name) && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\:\x00")
}

func writeFile(root, name string, data []byte) error {
	if !safeName(name) {
		return fmt.Errorf("unsafe output path %q", name)
	}
	directory := root
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		directory = filepath.Join(directory, part)
		if err := os.Mkdir(directory, 0755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(directory)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("generated path crosses a non-directory or symlink: %s", directory)
		}
	}
	file, err := os.CreateTemp(directory, ".keel-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0644); err != nil {
		file.Close()
		return err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(root, filepath.FromSlash(name)))
}
