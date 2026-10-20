package payments

import (
	"encoding/json"
	"github.com/marcelsud/keel/examples/cinema/packages/shared-go"
	"github.com/marcelsud/keel/examples/cinema/services/checkout/internal/transport"
	"github.com/marcelsud/keel/sdk"
)

func Register(app *sdk.App, service *Service) error {
	return app.Action("pay", func(ctx *sdk.Context, raw json.RawMessage) (any, error) {
		var input Request
		if err := cinema.Decode(raw, &input); err != nil {
			return nil, err
		}
		return transport.Reply(service.Pay(ctx, input))
	})
}
