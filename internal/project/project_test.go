package project

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/marcelsud/keel/core"
	"github.com/marcelsud/keel/sdk"
)

const noteProto = `syntax = "proto3";
package notes.v1;
import "common/content.proto";
// Create notes locally.
service NotesService { rpc CreateNote(CreateNoteRequest) returns (Note); }
service LookupService { rpc FindNote(CreateNoteRequest) returns (Note); }
message CreateNoteRequest { common.Content content = 1; }
message Note { string id = 1; common.Content content = 2; }
`

func write(t *testing.T, filename, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, filename string) string {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func saveConfig(t *testing.T, root string, config Config) {
	t.Helper()
	data, err := marshalConfig(configName, config)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, configName), string(data))
}

func tooling(t *testing.T) (string, string, string) {
	t.Helper()
	if _, err := exec.LookPath("buf"); err != nil {
		t.Skip("install Buf to run local generation integration tests")
	}
	_, current, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "../.."))
	esPlugin := filepath.Join(root, "sdks/typescript/node_modules/.bin/protoc-gen-es")
	if _, err := os.Stat(esPlugin); err != nil {
		t.Skip("install TypeScript SDK development dependencies")
	}
	goPlugin := os.Getenv("KEEL_GO_PLUGIN")
	if goPlugin == "" {
		goPlugin, _ = exec.LookPath("protoc-gen-go")
	}
	if goPlugin == "" {
		goPlugin = filepath.Join(t.TempDir(), "protoc-gen-go")
		command := exec.Command("go", "build", "-o", goPlugin, "google.golang.org/protobuf/cmd/protoc-gen-go")
		command.Dir = root
		if result, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, result)
		}
	}
	return root, goPlugin, esPlugin
}

func workspace(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	common, notes, orders := filepath.Join(root, "common"), filepath.Join(root, "notes"), filepath.Join(root, "orders")
	write(t, filepath.Join(common, "proto/common/content.proto"), `syntax="proto3"; package common;
import "google/protobuf/timestamp.proto";
message Content { string text=1; google.protobuf.Timestamp created_at=2; }`)
	write(t, filepath.Join(notes, "proto/notes/v1/notes.proto"), noteProto)
	saveConfig(t, notes, Config{Dependencies: map[string]string{"common": "../common"}})
	write(t, filepath.Join(orders, "package.json"), `{"type":"module"}`)
	return common, notes, orders
}

func TestGenerateExplicitLanguageWithoutProjectFiles(t *testing.T) {
	_, goPlugin, esPlugin := tooling(t)
	for _, test := range []struct {
		name     string
		args     []string
		filename string
	}{
		{"typescript positional", []string{"typescript", "--es-plugin", esPlugin}, "generated/greet/v1/greet_keel.ts"},
		{"typescript after flags", []string{"--es-plugin", esPlugin, "typescript"}, "generated/greet/v1/greet_keel.ts"},
		{"typescript short flag", []string{"-lang", "typescript", "--es-plugin", esPlugin}, "generated/greet/v1/greet_keel.ts"},
		{"typescript long flag", []string{"--lang", "typescript", "--es-plugin", esPlugin}, "generated/greet/v1/greet_keel.ts"},
		{"typescript custom output", []string{"typescript", "--out", "src/generated", "--es-plugin", esPlugin}, "src/generated/greet/v1/greet_keel.ts"},
		{"go positional", []string{"go", "--go-package-prefix", "example.com/greet/generated", "--go-plugin", goPlugin}, "generated/greet/v1/greet.keel.go"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			write(t, filepath.Join(root, "proto/greet/v1/greet.proto"), `syntax = "proto3";
package greet.v1;
service GreeterService { rpc Greet(GreetRequest) returns (GreetReply); }
message GreetRequest { string name = 1; }
message GreetReply { string message = 1; }`)
			if err := Run(context.Background(), "generate", test.args, io.Discard); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(read(t, filepath.Join(root, test.filename)), "GreeterService") {
				t.Fatal("missing generated service")
			}
		})
	}
}

func TestGenerateRejectsInvalidLanguageArguments(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"python"}, "unsupported language"},
		{[]string{"typescript", "--lang", "go"}, "conflicts with --lang"},
		{[]string{"typescript", "go"}, "unexpected positional arguments"},
	} {
		if err := Run(context.Background(), "generate", test.args, io.Discard); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%v: got %v, want %q", test.args, err, test.want)
		}
	}
}

func TestUseHelpExplainsWorkflow(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	var output bytes.Buffer
	if err := Run(context.Background(), "use", []string{"--help"}, &output); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"Usage: keel use <local-project-directory> [options]",
		"contracts and generate bindings", "keel.toml", "keel.json",
		"keel use ../users\n", "keel use ../users --as identity",
		"keel use ../users --lang typescript --out src/generated",
		"keel use ../users --lang go --out generated", "Options:",
	} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("help is missing %q:\n%s", text, &output)
		}
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 {
		t.Fatal("help changed the project directory", files, err)
	}
}

func TestGenerateLocalDependenciesAndCompileBothLanguages(t *testing.T) {
	repo, goPlugin, esPlugin := tooling(t)
	_, notes, orders := workspace(t)
	t.Chdir(orders)
	if err := Run(context.Background(), "use", []string{notes, "--es-plugin", esPlugin}, io.Discard); err != nil {
		t.Fatal(err)
	}
	config, err := load(orders)
	if err != nil {
		t.Fatal(err)
	}
	if config.Dependencies["notes"] != "../notes" {
		t.Fatal(config.Dependencies)
	}
	output := filepath.Join(orders, "generated")
	if !strings.Contains(read(t, filepath.Join(output, "notes/v1/notes_keel.ts")), "LookupServiceContract") {
		t.Fatal("multiple services were not generated")
	}
	if !strings.Contains(read(t, filepath.Join(output, "notes/v1/notes_pb.ts")), "Create notes locally.") {
		t.Fatal("generated bindings lost contract documentation")
	}
	if _, err := os.Stat(filepath.Join(output, "common/content_pb.ts")); err != nil {
		t.Fatal("missing transitive import", err)
	}
	if err := os.Symlink(filepath.Join(repo, "sdks/typescript/node_modules"), filepath.Join(orders, "node_modules")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(orders, "tsconfig.json"), `{"compilerOptions":{"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext","strict":true,"skipLibCheck":true,"noEmit":true},"include":["generated/**/*.ts"]}`)
	command := exec.Command("node", filepath.Join(repo, "sdks/typescript/node_modules/typescript/bin/tsc"), "-p", "tsconfig.json")
	command.Dir = orders
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated TypeScript: %v: %s", err, result)
	}
	write(t, filepath.Join(orders, "go.mod"), fmt.Sprintf("module example.com/orders\n\ngo 1.26.0\n\nrequire github.com/marcelsud/keel v0.0.0\nreplace github.com/marcelsud/keel => %s\n", filepath.ToSlash(repo)))
	config.Targets = []Target{{Language: "go", Output: "go-generated", GoPlugin: goPlugin}}
	if _, _, err := Generate(context.Background(), orders, config, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, filepath.Join(orders, "go-generated/notes/v1/notes.pb.go")), "example.com/orders/go-generated/common") {
		t.Fatal("consumer Go import paths were not inferred")
	}
	command = exec.Command("go", "test", "-mod=mod", "./go-generated/...")
	command.Dir = orders
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated Go: %v: %s", err, result)
	}
}

func TestLocalGenerationUpdatesAndPreservesUserFiles(t *testing.T) {
	_, _, esPlugin := tooling(t)
	_, notes, orders := workspace(t)
	config := Config{Dependencies: map[string]string{"notes": "../notes"}, Targets: []Target{{Language: "typescript", Output: "generated", ESPlugin: esPlugin}}}
	old := filepath.Join(notes, "proto/notes/v1/old.proto")
	write(t, old, `syntax="proto3"; package notes.v1; message Old { string value=1; }`)
	if _, _, err := Generate(context.Background(), orders, config, io.Discard); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(orders, "generated")
	before := read(t, filepath.Join(output, "notes/v1/notes_keel.ts"))
	write(t, filepath.Join(output, "keep.txt"), "user-owned")
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(noteProto, "string id = 1;", "string id = 1; string title = 3;", 1)
	write(t, filepath.Join(notes, "proto/notes/v1/notes.proto"), changed)
	if _, _, err := Generate(context.Background(), orders, config, io.Discard); err != nil {
		t.Fatal(err)
	}
	if before == read(t, filepath.Join(output, "notes/v1/notes_keel.ts")) {
		t.Fatal("local contract did not update")
	}
	if _, err := os.Stat(filepath.Join(output, "notes/v1/old_pb.ts")); !os.IsNotExist(err) {
		t.Fatal("obsolete generated file survived", err)
	}
	if read(t, filepath.Join(output, "keep.txt")) != "user-owned" {
		t.Fatal("deleted user file")
	}
	manifest := read(t, filepath.Join(output, manifestName))
	write(t, filepath.Join(notes, "proto/notes/v1/notes.proto"), "invalid proto")
	if _, _, err := Generate(context.Background(), orders, config, io.Discard); err == nil {
		t.Fatal("invalid schema accepted")
	}
	if manifest != read(t, filepath.Join(output, manifestName)) {
		t.Fatal("failed generation changed output")
	}
}

func TestLocalDependencyFailuresAndOutputProtection(t *testing.T) {
	_, _, esPlugin := tooling(t)
	common, notes, orders := workspace(t)
	config := Config{Dependencies: map[string]string{"notes": "../notes"}, Targets: []Target{{Language: "typescript", Output: "generated", ESPlugin: esPlugin}}}
	protected := filepath.Join(orders, "generated/notes/v1/notes_keel.ts")
	write(t, protected, "user source")
	if _, _, err := Generate(context.Background(), orders, config, io.Discard); err == nil || !strings.Contains(err.Error(), "non-generated") {
		t.Fatal(err)
	}
	if read(t, protected) != "user source" {
		t.Fatal("overwrote user file")
	}
	if _, err := os.Stat(filepath.Join(orders, "generated/common/content_pb.ts")); !os.IsNotExist(err) {
		t.Fatal("failed generation wrote partial output")
	}
	write(t, filepath.Join(orders, "proto/common/content.proto"), `syntax="proto3"; package common; message Different {}`)
	if _, _, err := Generate(context.Background(), orders, config, io.Discard); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatal(err)
	}
	saveConfig(t, common, Config{Dependencies: map[string]string{"notes": "../notes"}})
	if _, err := graph(orders, config); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatal(err)
	}
	_ = notes
}

func TestGenerationRejectsSymlinkOutputs(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "nested")); err != nil {
		t.Fatal(err)
	}
	if err := validateOutput(Target{Language: "typescript", Output: root}, map[string]string{"nested/file.ts": "generated"}); err == nil {
		t.Fatal("accepted output symlink")
	}
	write(t, filepath.Join(root, manifestName), `{"language":"typescript","files":["../outside.ts"]}`)
	if _, err := previousFiles(Target{Language: "typescript", Output: root}); err == nil {
		t.Fatal("accepted unsafe manifest")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for development process")
}

func TestDevHelper(t *testing.T) {
	if os.Getenv("KEEL_DEV_HELPER") != "1" {
		return
	}
	app, err := sdk.Connect(sdk.Options{Name: "dev-helper", URL: os.Getenv("KEEL_URL"), Token: os.Getenv("KEEL_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Stop()
	if err := app.KV.Set(context.Background(), "ready", true); err != nil {
		t.Fatal(err)
	}
	var data []byte
	if generated := os.Getenv("KEEL_DEV_GENERATED"); generated != "" {
		data, err = os.ReadFile(generated)
		if err != nil {
			t.Fatal(err)
		}
	}
	write(t, os.Getenv("KEEL_DEV_MARKER"), strconv.Itoa(os.Getpid())+"\n"+string(data))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}

func TestDevStartsWithoutContractsAndGeneratesWhenAdded(t *testing.T) {
	_, _, esPlugin := tooling(t)
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Targets: []Target{{Language: "typescript", Output: "generated", ESPlugin: esPlugin}}, Command: []string{executable, "-test.run=^TestDevHelper$"}}
	saveConfig(t, root, config)
	marker := filepath.Join(root, "ready.log")
	t.Setenv("KEEL_DEV_HELPER", "1")
	t.Setenv("KEEL_DEV_MARKER", marker)
	t.Setenv("KEEL_DEV_GENERATED", "")
	t.Setenv("KEEL_URL", "")
	t.Setenv("KEEL_TOKEN", "dev-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var logs bytes.Buffer
	go func() {
		done <- Dev(ctx, root, config, DevOptions{Core: core.Options{Port: -1, DataDir: filepath.Join(root, ".keel/data"), Token: "dev-token"}}, &logs)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err, logs.String())
			}
		case <-time.After(10 * time.Second):
			t.Error("dev did not stop")
		}
	})
	var first string
	waitFor(t, func() bool { data, _ := os.ReadFile(marker); first = string(data); return len(first) > 0 })
	write(t, filepath.Join(root, "main.ts"), "// source edit before adding a contract\n")
	waitFor(t, func() bool { data, _ := os.ReadFile(marker); return len(data) > 0 && string(data) != first })
	second := read(t, marker)
	config.Command = append(config.Command, "-test.v")
	saveConfig(t, root, config)
	waitFor(t, func() bool { data, _ := os.ReadFile(marker); return len(data) > 0 && string(data) != second })
	second = read(t, marker)
	if _, err := os.Stat(filepath.Join(root, "generated")); !os.IsNotExist(err) {
		t.Fatal("created bindings without a contract", err)
	}
	write(t, filepath.Join(root, "proto/greeting.proto"), `syntax="proto3"; package greeting; message Greeting { string message=1; }`)
	waitFor(t, func() bool { data, _ := os.ReadFile(marker); return len(data) > 0 && string(data) != second })
	if _, err := os.Stat(filepath.Join(root, "generated/greeting_pb.ts")); err != nil {
		t.Fatal("adding the first contract did not generate bindings", err)
	}
}

func TestDevStartsEnvironmentRegeneratesAndRestarts(t *testing.T) {
	_, _, esPlugin := tooling(t)
	_, notes, orders := workspace(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Dependencies: map[string]string{"notes": "../notes"}, Targets: []Target{{Language: "typescript", Output: "generated", ESPlugin: esPlugin}}, Command: []string{executable, "-test.run=^TestDevHelper$"}}
	saveConfig(t, orders, config)
	marker := filepath.Join(orders, "ready.log")
	generated := filepath.Join(orders, "generated/notes/v1/notes_keel.ts")
	t.Setenv("KEEL_DEV_HELPER", "1")
	t.Setenv("KEEL_DEV_MARKER", marker)
	t.Setenv("KEEL_DEV_GENERATED", generated)
	t.Setenv("KEEL_URL", "")
	t.Setenv("KEEL_TOKEN", "dev-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var logs bytes.Buffer
	go func() {
		done <- Dev(ctx, orders, config, DevOptions{Core: core.Options{Port: -1, DataDir: filepath.Join(orders, ".keel/data"), Token: "dev-token"}}, &logs)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err, logs.String())
			}
		case <-time.After(10 * time.Second):
			t.Error("dev did not stop")
		}
	})
	var first string
	waitFor(t, func() bool { data, _ := os.ReadFile(marker); first = string(data); return len(first) > 0 })
	write(t, filepath.Join(notes, "proto/notes/v1/notes.proto"), strings.Replace(noteProto, "string id = 1;", "string id = 1; string title = 3;", 1))
	waitFor(t, func() bool { data, _ := os.ReadFile(marker); return len(data) > 0 && string(data) != first })
	second := read(t, marker)
	if strings.Split(first, "\n")[0] == strings.Split(second, "\n")[0] {
		t.Fatal("service was not restarted")
	}
	if !strings.Contains(second, read(t, generated)) {
		t.Fatal("service restarted before generation finished")
	}
	write(t, generated, read(t, generated)+"\n// local edit\n")
	time.Sleep(700 * time.Millisecond)
	if second != read(t, marker) {
		t.Fatal("generated outputs caused a restart loop")
	}
	contract := filepath.Join(notes, "proto/notes/v1/notes.proto")
	write(t, contract, "invalid proto")
	time.Sleep(700 * time.Millisecond)
	if second != read(t, marker) {
		t.Fatal("invalid contract stopped the previous service")
	}
	write(t, contract, strings.Replace(noteProto, "string id = 1;", "string id = 1; string recovered = 3;", 1))
	waitFor(t, func() bool { data, _ := os.ReadFile(marker); return len(data) > 0 && string(data) != second })
	if !strings.Contains(read(t, marker), read(t, generated)) {
		t.Fatal("recovered service did not read the regenerated contract")
	}
}
