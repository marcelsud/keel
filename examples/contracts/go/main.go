package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	notesv1 "github.com/marcelsud/keel/gen/go/notes/v1"
	notesv2 "github.com/marcelsud/keel/gen/go/notes/v2"
	"github.com/marcelsud/keel/sdk"
)

type notesV1 struct{}
type notesV2 struct{}

func create(ctx *sdk.Context, content *notesv2.Content) (*notesv2.Note, error) {
	if content == nil || strings.TrimSpace(content.GetText()) == "" {
		return nil, &sdk.Error{Code: 400, Message: "content.text must not be empty"}
	}
	document := map[string]any{"text": content.GetText()}
	if content.Title != nil {
		document["title"] = content.GetTitle()
	}
	id, err := ctx.Collection.Insert(ctx, "notes", document)
	if err != nil {
		return nil, err
	}
	return &notesv2.Note{Id: id, Content: content}, nil
}

func (notesV2) CreateNote(ctx *sdk.Context, input *notesv2.CreateNoteRequest) (*notesv2.Note, error) {
	return create(ctx, input.GetContent())
}

// The v1 adapter preserves the old request and response while sharing storage logic.
func (notesV1) CreateNote(ctx *sdk.Context, input *notesv1.CreateNoteRequest) (*notesv1.Note, error) {
	note, err := create(ctx, &notesv2.Content{Text: input.GetBody()})
	if err != nil {
		return nil, err
	}
	return &notesv1.Note{Id: note.GetId(), Body: note.GetContent().GetText()}, nil
}

func main() {
	client := flag.Bool("client", false, "call the versioned services and exit")
	flag.Parse()
	app, err := sdk.Connect(sdk.Options{Name: "notes-go", URL: os.Getenv("KEEL_URL"), Token: os.Getenv("KEEL_TOKEN")})
	if err != nil {
		log.Fatal(err)
	}
	defer app.Stop()
	if *client {
		v1, err := notesv1.NewNotesServiceClient(app).CreateNote(context.Background(), &notesv1.CreateNoteRequest{Body: "Go client using v1"})
		if err != nil {
			log.Fatal(err)
		}
		v2, err := notesv2.NewNotesServiceClient(app).CreateNote(context.Background(), &notesv2.CreateNoteRequest{Content: &notesv2.Content{Text: "Go client using v2"}})
		if err != nil {
			log.Fatal(err)
		}
		encoded, err := json.Marshal(map[string]any{"v1": v1, "v2": v2})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(string(encoded))
		return
	}
	state := sdk.Deprecated
	if os.Getenv("KEEL_RETIRE_V1") == "1" {
		state = sdk.Retired
	}
	if err = notesv1.RegisterNotesService(app, notesV1{}, sdk.ContractOptions{State: state, Replacement: "notes.v2.NotesService"}); err != nil {
		log.Fatal(err)
	}
	if err = notesv2.RegisterNotesService(app, notesV2{}, sdk.ContractOptions{}); err != nil {
		log.Fatal(err)
	}
	if err = app.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	log.Print("notes-go ready: v1=" + state + ", v2=active")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}
