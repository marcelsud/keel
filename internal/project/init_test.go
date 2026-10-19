package project

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			config, err := load(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(config.Command) == 0 || len(config.Targets) != 0 {
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
				if manifest.Dependencies["@marcelsud/keel.ts"] != "0.2.0" || strings.Join(config.Command, " ") != "bun run service.ts" {
					t.Fatal(manifest, config)
				}
				for _, filename := range []string{"service.ts", "client.ts"} {
					if _, err := os.Stat(filepath.Join(root, filename)); err != nil {
						t.Fatal("missing starter file", filename, err)
					}
				}
				if _, err := os.Stat(filepath.Join(root, "main.ts")); !os.IsNotExist(err) {
					t.Fatal("starter still contains a combined entrypoint", err)
				}
			} else if config.Command[0] != "go" {
				t.Fatal(config)
			}
			if !strings.Contains(output.String(), "keel dev") {
				t.Fatal("missing next step", output.String())
			}
		})
	}
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
