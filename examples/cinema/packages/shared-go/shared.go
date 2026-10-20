// Package cinema contains the JSON shapes shared by the Go cinema services.
package cinema

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/marcelsud/keel/sdk"
)

type Seat struct {
	ID         string `json:"id"`
	Category   string `json:"category"`
	PriceMinor int32  `json:"priceMinor"`
	Blocked    bool   `json:"blocked"`
}

type Screening struct {
	ID             string `json:"id"`
	CinemaID       string `json:"cinemaId"`
	AuditoriumID   string `json:"auditoriumId"`
	MovieID        string `json:"movieId"`
	StartsAtMs     int64  `json:"startsAtMs"`
	EndsAtMs       int64  `json:"endsAtMs"`
	Currency       string `json:"currency"`
	HoldMs         int64  `json:"holdMs"`
	CancelCutoffMs int64  `json:"cancelCutoffMs"`
	Canceled       bool   `json:"canceled"`
	Seats          []Seat `json:"seats"`
}

type Event struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	ScreeningID  string `json:"screeningId"`
	OrderID      string `json:"orderId"`
	CustomerID   string `json:"customerId"`
	Status       string `json:"status"`
	RefundStatus string `json:"refundStatus"`
}

func Decode(input json.RawMessage, output any) error {
	if err := json.Unmarshal(input, output); err != nil {
		return &sdk.Error{Code: 400, Message: "invalid request fields"}
	}
	return nil
}

func Run(name string, register func(*sdk.App) error, background func(context.Context, *sdk.App)) {
	app, err := sdk.Connect(sdk.Options{Name: name, URL: os.Getenv("KEEL_URL"), Token: os.Getenv("KEEL_TOKEN"), User: os.Getenv("KEEL_USER")})
	if err != nil {
		log.Fatal(err)
	}
	defer app.Stop()
	if err := register(app); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Start(ctx); err != nil {
		log.Fatal(err)
	}
	var done chan struct{}
	if background != nil {
		done = make(chan struct{})
		go func() { defer close(done); background(ctx, app) }()
	}
	fmt.Println(name + " ready")
	<-ctx.Done()
	if done != nil {
		<-done
	}
}
