package main

import (
	"context"
	"github.com/marcelsud/keel/examples/cinema/packages/shared-go"
	"github.com/marcelsud/keel/examples/cinema/services/booking"
	"github.com/marcelsud/keel/sdk"
)

func main() {
	var module *booking.Module
	cinema.Run("cinema-booking", func(app *sdk.App) error {
		module = booking.NewModule(app)
		return module.Register(app)
	}, func(ctx context.Context, _ *sdk.App) { module.Run(ctx) })
}
