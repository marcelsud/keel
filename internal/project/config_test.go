package project

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadConfigFormats(t *testing.T) {
	want := Config{
		Command: []string{"bun", "run", "src/service.ts"}, Proto: "contracts/proto",
		Services: []string{"notes"}, SQL: map[string]string{"notes": "migrations"},
		Dependencies: map[string]string{"users.v1": "../users"},
		Targets: []Target{{Language: "typescript", Output: "src/generated", ESPlugin: "tools/protoc-gen-es"},
			{Language: "go", Output: "generated/go", GoPackagePrefix: "example.com/app/generated/go", GoPlugin: "tools/protoc-gen-go"}},
	}
	for filename, source := range map[string]string{
		configName: `# A project configuration with comments.
command = ["bun", "run", "src/service.ts"]
proto = "contracts/proto"
services = ["notes"]
[sql]
notes = "migrations"
[dependencies]
"users.v1" = "../users"
[[targets]]
language = "typescript"
out = "src/generated"
esPlugin = "tools/protoc-gen-es"
[[targets]]
language = "go"
out = "generated/go"
goPackagePrefix = "example.com/app/generated/go"
goPlugin = "tools/protoc-gen-go"
`,
		jsonConfigName: `{"command":["bun","run","src/service.ts"],"proto":"contracts/proto","services":["notes"],"sql":{"notes":"migrations"},"dependencies":{"users.v1":"../users"},"targets":[{"language":"typescript","out":"src/generated","esPlugin":"tools/protoc-gen-es"},{"language":"go","out":"generated/go","goPackagePrefix":"example.com/app/generated/go","goPlugin":"tools/protoc-gen-go"}]}`,
	} {
		t.Run(filename, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, filename), source)
			got, err := load(root)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("loaded %+v, want %+v: %v", got, want, err)
			}
			data, err := marshalConfig(filename, got)
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(root, filename), string(data))
			got, err = load(root)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip changed configuration: %+v, %v", got, err)
			}
		})
	}
	t.Run("no configuration", func(t *testing.T) {
		got, err := load(t.TempDir())
		if err != nil || !reflect.DeepEqual(got, Config{}) {
			t.Fatal(got, err)
		}
	})
	t.Run("ambiguous configuration", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, configName), "invalid TOML")
		write(t, filepath.Join(root, jsonConfigName), "{}")
		if _, err := load(root); err == nil || !strings.Contains(err.Error(), "both keel.toml and keel.json") {
			t.Fatal("expected an ambiguity error before parsing", err)
		}
	})
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	for _, tc := range []struct{ name, filename, source string }{
		{"TOML syntax", configName, "command = ["},
		{"TOML unknown field", configName, "commnad = []"},
		{"TOML unknown target field", configName, "[[targets]]\nlanguage = 'go'\noutput = 'generated'"},
		{"TOML wrong type", configName, "command = 'bun'"},
		{"JSON syntax", jsonConfigName, "{"},
		{"JSON unknown field", jsonConfigName, `{"commnad":[]}`},
		{"JSON unknown target field", jsonConfigName, `{"targets":[{"output":"generated"}]}`},
		{"JSON wrong type", jsonConfigName, `{"command":"bun"}`},
		{"JSON trailing object", jsonConfigName, "{} {}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, tc.filename), tc.source)
			if _, err := load(root); err == nil || !strings.Contains(err.Error(), tc.filename) {
				t.Fatal("expected a configuration error with its filename", err)
			}
		})
	}
}

func TestUsePreservesConfigFormat(t *testing.T) {
	_, _, esPlugin := tooling(t)
	for _, filename := range []string{"", configName, jsonConfigName} {
		t.Run("existing="+filename, func(t *testing.T) {
			parent := t.TempDir()
			notes, orders := filepath.Join(parent, "notes"), filepath.Join(parent, "orders")
			write(t, filepath.Join(notes, "contracts/greet/v1/greet.proto"), greetingProto)
			dependencyFilename := jsonConfigName
			if filename == jsonConfigName {
				dependencyFilename = configName
			}
			data, err := marshalConfig(dependencyFilename, Config{Proto: "contracts"})
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(notes, dependencyFilename), string(data))
			write(t, filepath.Join(orders, "package.json"), `{"type":"module"}`)
			before := Config{Command: []string{"bun", "run", "src/service.ts"}, SQL: map[string]string{"orders": "migrations"}}
			if filename != "" {
				data, err := marshalConfig(filename, before)
				if err != nil {
					t.Fatal(err)
				}
				write(t, filepath.Join(orders, filename), string(data))
			}
			t.Chdir(orders)
			if err := Run(context.Background(), "use", []string{"../notes", "--es-plugin", esPlugin}, io.Discard); err != nil {
				t.Fatal(err)
			}
			after, err := load(orders)
			if err != nil || after.Dependencies["notes"] != "../notes" {
				t.Fatal(after, err)
			}
			if filename != "" && (!reflect.DeepEqual(after.Command, before.Command) || !reflect.DeepEqual(after.SQL, before.SQL)) {
				t.Fatal("use discarded existing configuration", after)
			}
			if filename == "" {
				filename = configName
			}
			other := jsonConfigName
			if filename == jsonConfigName {
				other = configName
			}
			if _, err := os.Stat(filepath.Join(orders, other)); !os.IsNotExist(err) {
				t.Fatal("use created a conflicting configuration", err)
			}
			if _, err := os.Stat(filepath.Join(orders, "generated/greet/v1/greet_keel.ts")); err != nil {
				t.Fatal("mixed-format dependency was not generated", err)
			}
		})
	}
}

func TestFingerprintWatchesBothConfigFormats(t *testing.T) {
	root, dependency := t.TempDir(), t.TempDir()
	projects := []localProject{{root: root}, {root: dependency}}
	previous, err := fingerprint(root, projects, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{root, dependency} {
		for _, filename := range []string{configName, jsonConfigName} {
			for _, value := range []string{"first edit", "second edit", ""} {
				path := filepath.Join(directory, filename)
				if value == "" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else {
					write(t, path, value)
				}
				next, err := fingerprint(root, projects, nil)
				if err != nil || next == previous {
					t.Fatalf("did not detect a change to %s: %v", path, err)
				}
				previous = next
			}
		}
	}
}
