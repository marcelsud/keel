package cancellations

import (
	"context"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/orders"
)

func (s Feature) CancelScreening(ctx context.Context, input *pb.ScreeningRef) (*pb.Result, error) {
	_, err := s.Orders.Mutate(ctx, input.ScreeningId, func(state *orders.State) error {
		state.Canceled = true
		for _, order := range state.Orders {
			if order.Status != "CANCELED" && order.Status != "EXPIRED" {
				orders.Cancel(state, order)
			}
		}
		return nil
	})
	return &pb.Result{Canceled: true}, err
}
