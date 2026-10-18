package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/marcelsud/keel/sdk"
)

func main() {
	app, err := sdk.Connect(sdk.Options{Name: "notes-go", URL: os.Getenv("KEEL_URL"), Token: os.Getenv("KEEL_TOKEN")})
	if err != nil {
		log.Fatal(err)
	}
	defer app.Stop()
	err = app.Action("createNote", func(ctx *sdk.Context, raw json.RawMessage) (any, error) {
		var input struct {
			Body string `json:"body"`
		}
		if json.Unmarshal(raw, &input) != nil || input.Body == "" {
			return nil, &sdk.Error{Code: 400, Message: "body must be a nonempty string"}
		}
		id, err := ctx.Collection.Insert(ctx, "notes", input)
		if err != nil {
			return nil, err
		}
		if err = ctx.Pubsub.Publish(ctx, "note.created", map[string]string{"id": id}); err != nil {
			return nil, err
		}
		return map[string]string{"id": id, "body": input.Body}, nil
	})
	if err != nil {
		log.Fatal(err)
	}
	if err = app.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}
