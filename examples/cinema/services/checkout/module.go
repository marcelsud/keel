package checkout

import (
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	pay "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/payments/v1"
	"github.com/marcelsud/keel/examples/cinema/services/checkout/internal/availability"
	"github.com/marcelsud/keel/examples/cinema/services/checkout/internal/cancellations"
	"github.com/marcelsud/keel/examples/cinema/services/checkout/internal/catalog"
	"github.com/marcelsud/keel/examples/cinema/services/checkout/internal/orders"
	"github.com/marcelsud/keel/examples/cinema/services/checkout/internal/payments"
	"github.com/marcelsud/keel/examples/cinema/services/checkout/internal/receipts"
	"github.com/marcelsud/keel/examples/cinema/services/checkout/internal/reservations"
	"github.com/marcelsud/keel/sdk"
)

func Register(app *sdk.App) error {
	booking := pb.NewBookingServiceClient(app)
	service := &payments.Service{Booking: booking, Payments: pay.NewPaymentsServiceClient(app)}
	registrations := []func() error{
		func() error { return reservations.Register(app, booking) },
		func() error { return orders.Register(app, booking) },
		func() error { return cancellations.Register(app, booking) },
		func() error { return availability.Register(app, booking) },
		func() error { return catalog.Register(app) },
		func() error { return receipts.Register(app) },
		func() error { return payments.Register(app, service) },
	}
	for _, register := range registrations {
		if err := register(); err != nil {
			return err
		}
	}
	return nil
}
