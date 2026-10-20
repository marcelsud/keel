package booking

import (
	"context"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	pay "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/payments/v1"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/availability"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/cancellations"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/catalog"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/orders"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/payments"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/reconciliation"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/reservations"
	"github.com/marcelsud/keel/sdk"
)

type server struct {
	*reservations.ReservationsHandler
	*cancellations.CancellationsHandler
	*availability.AvailabilityHandler
	*payments.PaymentsHandler
	*orders.OrdersHandler
}

var _ pb.BookingServiceServer = (*server)(nil)

type Module struct {
	server   *server
	payments *payments.Feature
	worker   *reconciliation.Worker
}

func NewModule(app *sdk.App) *Module {
	catalog := &catalog.Client{App: app}
	store := &orders.Store{Repository: &orders.Repository{SQL: app.SQL}, Catalog: catalog}
	payment := &payments.Feature{Orders: store, Provider: pay.NewPaymentsServiceClient(app)}
	return &Module{
		server: &server{
			ReservationsHandler:  &reservations.ReservationsHandler{Feature: &reservations.Feature{Orders: store, Catalog: catalog}},
			CancellationsHandler: &cancellations.CancellationsHandler{Feature: &cancellations.Feature{Orders: store}},
			AvailabilityHandler:  &availability.AvailabilityHandler{Feature: &availability.Feature{Orders: store, Catalog: catalog}},
			PaymentsHandler:      &payments.PaymentsHandler{Feature: payment},
			OrdersHandler:        &orders.OrdersHandler{Store: store},
		},
		payments: payment,
		worker:   &reconciliation.Worker{Orders: store, Catalog: catalog, Payments: payment, Pubsub: app.Pubsub},
	}
}
func (m *Module) Register(app *sdk.App) error {
	if err := pb.RegisterBookingService(app, m.server, sdk.ContractOptions{}); err != nil {
		return err
	}
	return m.payments.RegisterSubscription(app)
}
func (m *Module) Run(ctx context.Context) { m.worker.Run(ctx) }
