package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInitCreatesProjectsWithoutContracts(t *testing.T) {
	for _, language := range []string{"typescript", "go"} {
		t.Run(language, func(t *testing.T) {
			parent := t.TempDir()
			t.Chdir(parent)
			var output bytes.Buffer
			if err := Init(context.Background(), []string{"greeter", "--lang", language, "--skip-install"}, &output); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(parent, "greeter")
			if _, err := os.Stat(filepath.Join(root, "keel.toml")); err != nil {
				t.Fatal("init must create keel.toml", err)
			}
			if _, err := os.Stat(filepath.Join(root, "keel.json")); !os.IsNotExist(err) {
				t.Fatal("init created a JSON configuration", err)
			}
			config, err := load(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(config.Command) == 0 {
				t.Fatal(config)
			}
			projects, err := graph(root, config)
			if err != nil {
				t.Fatal(err)
			}
			files, err := sources(projects)
			if err != nil || len(files) != 0 {
				t.Fatalf("starter unexpectedly requires contracts: %v, %v", files, err)
			}
			if language == "typescript" {
				var manifest struct{ Dependencies map[string]string }
				if err := json.Unmarshal([]byte(read(t, filepath.Join(root, "package.json"))), &manifest); err != nil {
					t.Fatal(err)
				}
				if manifest.Dependencies["@marcelsud/keel.ts"] != "0.2.0" || strings.Join(config.Command, " ") != "bun run src/service.ts" {
					t.Fatal(manifest, config)
				}
				for _, filename := range []string{"src/service.ts", "src/client.ts"} {
					if _, err := os.Stat(filepath.Join(root, filename)); err != nil {
						t.Fatal("missing starter file", filename, err)
					}
				}
				files, err := filepath.Glob(filepath.Join(root, "*.ts"))
				if err != nil || len(files) != 0 {
					t.Fatal("starter contains TypeScript outside src", files, err)
				}
				var tsconfig struct{ Include []string }
				if err := json.Unmarshal([]byte(read(t, filepath.Join(root, "tsconfig.json"))), &tsconfig); err != nil {
					t.Fatal(err)
				}
				if len(tsconfig.Include) != 1 || tsconfig.Include[0] != "src/**/*.ts" {
					t.Fatal(tsconfig)
				}
			} else if config.Command[0] != "go" || len(config.Targets) != 0 {
				t.Fatal(config)
			}
			if !strings.Contains(output.String(), "keel dev") {
				t.Fatal("missing next step", output.String())
			}
		})
	}
}

func TestInitWithPublishedSDKs(t *testing.T) {
	if os.Getenv("KEEL_PUBLISHED_SDK_TEST") != "1" {
		t.Skip("set KEEL_PUBLISHED_SDK_TEST=1 to install and compile starters against published SDKs")
	}
	for _, language := range []string{"typescript", "go"} {
		for _, starter := range []string{"empty", "json", "protobuf"} {
			t.Run(language+"/"+starter, func(t *testing.T) {
				t.Chdir(t.TempDir())
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				var output bytes.Buffer
				args := []string{"--template", starter, "--yes"}
				command := []string{"bun", "run", "check"}
				if language == "go" {
					args = append(args, "--lang", "go")
					command = []string{"go", "build", "-buildvcs=false", "-o", filepath.Join(t.TempDir(), "greeter"), "."}
				}
				if err := Init(ctx, args, &output); err != nil {
					t.Fatalf("initialize with published SDK: %v\n%s", err, &output)
				}
				check := exec.CommandContext(ctx, command[0], command[1:]...)
				if result, err := check.CombinedOutput(); err != nil {
					t.Fatalf("compile with published SDK: %v\n%s", err, result)
				}
			})
		}
	}
}

func TestInitInteractiveChoices(t *testing.T) {
	for _, tc := range []struct {
		name, answers, directory, language, starter string
	}{
		{"defaults", "\n\n\n", ".", "typescript", "json"},
		{"TypeScript empty", "\ntypescript\nempty\n", ".", "typescript", "empty"},
		{"new Go project", "new-service\ngo\nempty\n", "new-service", "go", "empty"},
		{"TypeScript protobuf", "proto-ts\ntypescript\nprotobuf\n", "proto-ts", "typescript", "protobuf"},
		{"Go protobuf", "proto-go\nGo\nprotobuf\n", "proto-go", "go", "protobuf"},
		{"retry invalid answers", "\nrust\ngo\nother\nempty\n", ".", "go", "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			t.Chdir(parent)
			var output bytes.Buffer
			if err := initProject(context.Background(), []string{"--skip-install"}, strings.NewReader(tc.answers), &output, true); err != nil {
				t.Fatal(err, &output)
			}
			root := filepath.Join(parent, tc.directory)
			config, err := load(root)
			if err != nil {
				t.Fatal(err)
			}
			wantCommand := "bun run src/service.ts"
			filename := "src/service.ts"
			if tc.language == "go" {
				wantCommand, filename = "go run .", "main.go"
			}
			if strings.Join(config.Command, " ") != wantCommand {
				t.Fatal(config.Command)
			}
			if tc.language == "typescript" && (len(config.Targets) != 1 || config.Targets[0].Language != "typescript" || config.Targets[0].Output != "src/generated") {
				t.Fatal("TypeScript bindings must be generated under src", config.Targets)
			}
			source := read(t, filepath.Join(root, filename))
			if strings.Contains(source, "{{name}}") {
				t.Fatal("project name was not substituted")
			}
			_, protoErr := os.Stat(filepath.Join(root, "proto/greet/v1/greet.proto"))
			if (protoErr == nil) != (tc.starter == "protobuf") {
				t.Fatal("wrong contract files", protoErr)
			}
			if tc.starter == "empty" {
				if strings.Contains(source, "greet") || strings.Contains(source, "-client") {
					t.Fatal("empty service contains example code")
				}
				if _, err := os.Stat(filepath.Join(root, "src/client.ts")); !os.IsNotExist(err) {
					t.Fatal("empty service contains client", err)
				}
			}
			if tc.starter == "protobuf" && !strings.Contains(output.String(), "keel generate") {
				t.Fatal("missing generation step")
			}
			for _, question := range []string{"Project directory", "Language", "Starter"} {
				if !strings.Contains(output.String(), question) {
					t.Fatalf("missing prompt %q", question)
				}
			}
		})
	}
}

func TestInitUnattendedAndExplicitOptionsDoNotPrompt(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		interactive bool
	}{
		{"yes", []string{"--yes", "--skip-install"}, true},
		{"short yes", []string{"-y", "--skip-install"}, true},
		{"redirected stdin", []string{"--skip-install"}, false},
		{"explicit options", []string{"child", "--lang", "go", "--template", "json", "--skip-install"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			var output bytes.Buffer
			if err := initProject(context.Background(), tc.args, strings.NewReader(""), &output, tc.interactive); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.String(), "Project directory") || strings.Contains(output.String(), "Language (") || strings.Contains(output.String(), "Starter (") {
				t.Fatal("unattended init prompted", &output)
			}
		})
	}
}

func TestInitInterruptedPromptsLeaveNoFiles(t *testing.T) {
	t.Run("EOF", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		if err := initProject(context.Background(), []string{"--skip-install"}, strings.NewReader("child\n"), io.Discard, true); err == nil {
			t.Fatal("accepted incomplete input")
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatal("interrupted prompt created files", entries, err)
		}
	})
	t.Run("context cancellation", func(t *testing.T) {
		root := t.TempDir()
		t.Chdir(root)
		reader, writer := io.Pipe()
		defer reader.Close()
		defer writer.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- initProject(ctx, []string{"--skip-install"}, reader, io.Discard, true) }()
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("prompt ignored cancellation")
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatal("canceled prompt created files", entries, err)
		}
	})
}

func TestInitPreservesExistingFiles(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	write(t, filepath.Join(root, "main.ts"), "user-owned")
	if err := Init(context.Background(), []string{"--skip-install"}, io.Discard); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "main.ts")) != "user-owned" {
		t.Fatal("overwrote the user's service")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal("failed init created partial files", entries, err)
	}
}

func TestInitValidatesArgumentsBeforeWriting(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"greeter", "--lang", "python", "--skip-install"},
		{"greeter", "another", "--skip-install"},
		{"invalid.name", "--skip-install"},
		{"keel-core", "--skip-install"},
		{"greeter", "--template", "missing", "--skip-install"},
	} {
		if err := Init(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	entries, err := os.ReadDir(".")
	if err != nil || len(entries) != 0 {
		t.Fatal("invalid arguments created files", entries, err)
	}
}

func TestDevWatchOnlyDoesNotRequireContractsOrGenerators(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, "dev", []string{"--watch-only"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := Run(ctx, "generate", nil, io.Discard); err == nil || !strings.Contains(err.Error(), "no contracts found") {
		t.Fatal("explicit generation should require contracts", err)
	}
}
