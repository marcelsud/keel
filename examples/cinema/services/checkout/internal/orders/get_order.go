package orders

import (
	"encoding/json"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/examples/cinema/services/checkout/internal/transport"
	"github.com/marcelsud/keel/sdk"
)

func Register(app *sdk.App, booking *pb.BookingServiceClient) error {
	return app.Action("getOrder", func(ctx *sdk.Context, raw json.RawMessage) (any, error) {
		input := new(pb.OrderRef)
		if err := transport.Parse(raw, input); err != nil {
			return nil, err
		}
		return transport.Reply(booking.GetOrder(ctx, input))
	})
}
