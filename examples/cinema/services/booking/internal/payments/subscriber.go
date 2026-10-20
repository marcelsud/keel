package payments

import (
	"encoding/json"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/examples/cinema/packages/shared-go"
	"github.com/marcelsud/keel/sdk"
)

func (s Feature) RegisterSubscription(app *sdk.App) error {
	return app.Subscription("cinema.payment", "cinema-booking-payment-results", func(ctx *sdk.Context, raw json.RawMessage) error {
		var callback struct {
			ScreeningID string `json:"screeningId"`
			OrderID     string `json:"orderId"`
			CustomerID  string `json:"customerId"`
		}
		if err := cinema.Decode(raw, &callback); err != nil {
			return err
		}
		// A callback wakes reconciliation; persisted provider state is authoritative.
		return s.Reconcile(ctx, &pb.OrderRef{ScreeningId: callback.ScreeningID, OrderId: callback.OrderID, CustomerId: callback.CustomerID})
	})
}
