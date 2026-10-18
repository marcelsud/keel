package sdk_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcelsud/keel/core"
	notesv1 "github.com/marcelsud/keel/gen/go/notes/v1"
	notesv2 "github.com/marcelsud/keel/gen/go/notes/v2"
	"github.com/marcelsud/keel/internal/protocol"
	"github.com/marcelsud/keel/sdk"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

type typedV2 struct{}

func (typedV2) CreateNote(ctx *sdk.Context, input *notesv2.CreateNoteRequest) (*notesv2.Note, error) {
	if input.GetContent().GetText() == "" {
		return nil, &sdk.Error{Code: 400, Message: "content.text is required"}
	}
	id, err := ctx.Collection.Insert(ctx, "typed-notes", map[string]any{"text": input.GetContent().GetText()})
	if err != nil {
		return nil, err
	}
	return &notesv2.Note{Id: id, Content: input.GetContent()}, nil
}

type typedV1 struct{}

func (typedV1) CreateNote(ctx *sdk.Context, input *notesv1.CreateNoteRequest) (*notesv1.Note, error) {
	note, err := notesv2.NewNotesServiceClient(ctx).CreateNote(ctx, &notesv2.CreateNoteRequest{Content: &notesv2.Content{Text: input.GetBody()}})
	if err != nil {
		return nil, err
	}
	return &notesv1.Note{Id: note.GetId(), Body: note.GetContent().GetText()}, nil
}

func registerTyped(t *testing.T, app *sdk.App, state string) {
	t.Helper()
	check(t, notesv1.RegisterNotesService(app, typedV1{}, sdk.ContractOptions{State: state, Replacement: "notes.v2.NotesService"}))
	check(t, notesv2.RegisterNotesService(app, typedV2{}, sdk.ContractOptions{}))
	check(t, app.Start(context.Background()))
}

func assertTypedCalls(t *testing.T, client *sdk.App) {
	t.Helper()
	ctx := context.Background()
	v1, err := notesv1.NewNotesServiceClient(client).CreateNote(ctx, &notesv1.CreateNoteRequest{Body: "old caller"})
	check(t, err)
	if v1.GetId() == "" || v1.GetBody() != "old caller" {
		t.Fatal(v1)
	}
	title := "Title"
	v2, err := notesv2.NewNotesServiceClient(client).CreateNote(ctx, &notesv2.CreateNoteRequest{Content: &notesv2.Content{Text: "new caller", Title: &title}})
	check(t, err)
	if v2.GetId() == "" || v2.GetContent().GetText() != "new caller" || v2.GetContent().GetTitle() != title {
		t.Fatal(v2)
	}
	_, err = notesv2.NewNotesServiceClient(client).CreateNote(ctx, &notesv2.CreateNoteRequest{})
	var failure *sdk.Error
	if !errors.As(err, &failure) || failure.Code != 400 {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestProtobufVersionsAndRetirement(t *testing.T) {
	c := startCore(t)
	server := connect(t, c, "protobuf-go")
	registerTyped(t, server, "")
	client := connect(t, c, "protobuf-caller")
	assertTypedCalls(t, client)
	ctx := context.Background()
	v1, err := client.FetchContract(ctx, "notes.v1.NotesService")
	check(t, err)
	if v1.State != sdk.Deprecated || v1.Replacement != "notes.v2.NotesService" {
		t.Fatal(v1.State, v1.Replacement)
	}
	v2, err := client.FetchContract(ctx, "notes.v2.NotesService")
	check(t, err)
	if v2.State != sdk.Active {
		t.Fatal(v2.State)
	}
	expected, err := base64.StdEncoding.DecodeString(notesv2.NotesServiceContract.Schema)
	check(t, err)
	if v2.SHA256 != protocol.Digest(expected) {
		t.Fatal("embedded descriptor changed")
	}
	var set descriptorpb.FileDescriptorSet
	check(t, proto.Unmarshal(v2.DescriptorSet, &set))
	if len(set.File) != 2 || set.File[0].GetName() != "notes/v2/content.proto" {
		t.Fatal("imported schema missing")
	}
	nc, err := nats.Connect(c.URL(), nats.Token("test-token"))
	check(t, err)
	defer nc.Close()
	response, err := nc.Request(protocol.RPCSubject("notes.v2.NotesService", "CreateNote"), []byte{255}, time.Second)
	check(t, err)
	if response.Header.Get("Nats-Service-Error-Code") != "400" {
		t.Fatal("malformed protobuf accepted")
	}
	check(t, server.Stop())
	retired := connect(t, c, "protobuf-retired")
	check(t, notesv1.RegisterNotesService(retired, nil, sdk.ContractOptions{State: sdk.Retired, Replacement: "notes.v2.NotesService"}))
	check(t, notesv2.RegisterNotesService(retired, typedV2{}, sdk.ContractOptions{}))
	check(t, retired.Start(ctx))
	_, err = notesv1.NewNotesServiceClient(client).CreateNote(ctx, &notesv1.CreateNoteRequest{Body: "old caller"})
	var failure *sdk.Error
	if !errors.As(err, &failure) || failure.Code != 410 {
		t.Fatalf("expected retired API error, got %v", err)
	}
	after, err := client.FetchContract(ctx, "notes.v1.NotesService")
	check(t, err)
	if after.State != sdk.Retired || after.SHA256 != v1.SHA256 {
		t.Fatal("retirement changed or removed the archived contract")
	}
	_, err = notesv2.NewNotesServiceClient(client).CreateNote(ctx, &notesv2.CreateNoteRequest{Content: &notesv2.Content{Text: "still supported"}})
	check(t, err)
}

func startTypeScriptContracts(t *testing.T, c *core.Core, retired bool) {
	t.Helper()
	file, err := filepath.Abs("../examples/contracts/typescript.ts")
	check(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "node", file)
	retire := "0"
	if retired {
		retire = "1"
	}
	command.Env = append(os.Environ(), "KEEL_URL="+c.URL(), "KEEL_TOKEN=test-token", "KEEL_RETIRE_V1="+retire)
	stdout, err := command.StdoutPipe()
	check(t, err)
	command.Stderr = os.Stderr
	check(t, command.Start())
	t.Cleanup(func() { cancel(); _ = command.Wait() })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "notes-typescript ready") {
				ready <- scanner.Text()
				return
			}
		}
	}()
	receive(t, ready)
}

func TestProtobufGoClientTypeScriptService(t *testing.T) {
	if os.Getenv("KEEL_TYPESCRIPT_TEST") != "1" {
		t.Skip("set KEEL_TYPESCRIPT_TEST=1 after building the TypeScript SDK")
	}
	c := startCore(t)
	startTypeScriptContracts(t, c, false)
	client := connect(t, c, "go-typed-caller")
	assertTypedCalls(t, client)
	schema, err := client.FetchContract(context.Background(), "notes.v2.NotesService")
	check(t, err)
	data, err := base64.StdEncoding.DecodeString(notesv2.NotesServiceContract.Schema)
	check(t, err)
	if schema.SHA256 != protocol.Digest(data) {
		t.Fatal("Go and TypeScript embedded different contracts")
	}
}

func TestProtobufTypeScriptRetirement(t *testing.T) {
	if os.Getenv("KEEL_TYPESCRIPT_TEST") != "1" {
		t.Skip("set KEEL_TYPESCRIPT_TEST=1 after building the TypeScript SDK")
	}
	c := startCore(t)
	startTypeScriptContracts(t, c, true)
	client := connect(t, c, "go-retired-caller")
	_, err := notesv1.NewNotesServiceClient(client).CreateNote(context.Background(), &notesv1.CreateNoteRequest{Body: "retired"})
	var failure *sdk.Error
	if !errors.As(err, &failure) || failure.Code != 410 {
		t.Fatalf("expected 410, got %v", err)
	}
	_, err = notesv2.NewNotesServiceClient(client).CreateNote(context.Background(), &notesv2.CreateNoteRequest{Content: &notesv2.Content{Text: "active"}})
	check(t, err)
}
