package pull

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	notesv2 "github.com/marcelsud/keel/gen/go/notes/v2"
	"github.com/marcelsud/keel/internal/protocol"
	"github.com/marcelsud/keel/sdk"
)

func testSchema(t *testing.T) sdk.Schema {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(notesv2.NotesServiceContract.Schema)
	if err != nil {
		t.Fatal(err)
	}
	return sdk.Schema{Service: "notes.v2.NotesService", SHA256: protocol.Digest(data), State: sdk.Active, DescriptorSet: data}
}

func TestGenerationFailuresLeaveNoOutput(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "generated")
	options := Options{Output: directory, Language: "typescript", ESPlugin: filepath.Join(t.TempDir(), "missing-plugin")}
	schema := testSchema(t)
	schema.SHA256 = strings.Repeat("0", 64)
	if err := Generate(context.Background(), schema, options); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("failed pull created output")
	}
	schema = testSchema(t)
	if err := Generate(context.Background(), schema, options); err == nil || !strings.Contains(err.Error(), "plugin") {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("failed plugin created output")
	}
}

func TestChangedContractRequiresUpdate(t *testing.T) {
	directory := t.TempDir()
	schema := testSchema(t)
	data, err := json.Marshal(lock{Service: schema.Service, SHA256: "old-hash", Language: "typescript"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, schema.Service+".lock.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	err = Generate(context.Background(), schema, Options{Output: directory, Language: "typescript"})
	if err == nil || !strings.Contains(err.Error(), "--update") {
		t.Fatal(err)
	}
}

func TestContractsCannotOverwriteEachOthersGeneratedFiles(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "other.v1.Service.lock.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	err := Generate(context.Background(), testSchema(t), Options{Output: directory, Language: "typescript"})
	if err == nil || !strings.Contains(err.Error(), "separate directory") {
		t.Fatal(err)
	}
}

func TestGeneratedFilesCannotEscapeOutput(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "nested")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../escape.go", "/escape.go", "nested/escape.go", "nested\\escape.go"} {
		if err := writeFile(root, name, []byte("must not escape")); err == nil {
			t.Fatalf("accepted unsafe path %q", name)
		}
	}
	files, err := os.ReadDir(outside)
	if err != nil || len(files) != 0 {
		t.Fatal("wrote outside output directory", err)
	}
}
