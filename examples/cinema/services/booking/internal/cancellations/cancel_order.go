package cancellations

import (
	"context"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/orders"
)

type Feature struct{ Orders *orders.Store }

func (s Feature) CancelOrder(ctx context.Context, ref *pb.OrderRef) (*pb.Order, error) {
	state, err := s.Orders.Mutate(ctx, ref.GetScreeningId(), func(state *orders.State) error {
		order, err := orders.Owned(state, ref)
		if err != nil {
			return err
		}
		if order.Status == "CANCELED" {
			return nil
		}
		if float64(orders.NowMs()) >= order.StartsAtMs-order.CancelCutoffMs {
			return orders.Failure(409, "cancellation cutoff has passed")
		}
		if order.Status == "EXPIRED" {
			return orders.Failure(409, "order already expired")
		}
		orders.Cancel(state, order)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state.Orders[ref.OrderId], nil
}
