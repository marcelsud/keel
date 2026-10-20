package reconciliation

import (
	"context"
	"fmt"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/catalog"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/orders"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/payments"
	"github.com/marcelsud/keel/sdk"
	"time"
)

type Worker struct {
	Orders   *orders.Store
	Catalog  *catalog.Client
	Payments *payments.Feature
	Pubsub   *sdk.Pubsub
}

func (s *Worker) Tick(ctx context.Context) error {
	screenings, err := s.Catalog.ListScreenings(ctx)
	if err != nil {
		return err
	}
	for _, screening := range screenings {
		exists, err := s.Orders.Repository.Exists(ctx, screening.ID)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		state, err := s.Orders.Mutate(ctx, screening.ID, func(*orders.State) error { return nil })
		if err != nil {
			return err
		}
		for _, order := range state.Orders {
			if err := s.Payments.Reconcile(ctx, &pb.OrderRef{ScreeningId: screening.ID, OrderId: order.Id, CustomerId: order.CustomerId}); err != nil && ctx.Err() != nil {
				return err
			}
		}
		state, err = s.Orders.Mutate(ctx, screening.ID, func(*orders.State) error { return nil })
		if err != nil {
			return err
		}
		for id, notification := range state.Outbox {
			if err := s.Pubsub.Publish(ctx, "cinema.booking", notification); err != nil {
				return err
			}
			if _, err := s.Orders.Mutate(ctx, screening.ID, func(state *orders.State) error { delete(state.Outbox, id); return nil }); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
				fmt.Println("cinema-booking reconciliation:", err)
			}
		}
	}
}
