package availability

import (
	"context"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/catalog"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/orders"
)

type Feature struct {
	Orders  *orders.Store
	Catalog *catalog.Client
}

func (s Feature) GetAvailability(ctx context.Context, input *pb.ScreeningRef) (*pb.Availability, error) {
	screening, err := s.Catalog.GetScreening(ctx, input.ScreeningId)
	if err != nil {
		return nil, err
	}
	state, err := s.Orders.Mutate(ctx, input.ScreeningId, func(*orders.State) error { return nil })
	if err != nil {
		return nil, err
	}
	taken := orders.Occupied(state)
	result := &pb.Availability{ScreeningId: input.ScreeningId, Canceled: state.Canceled || screening.Canceled}
	for _, seat := range screening.Seats {
		status := "AVAILABLE"
		if seat.Blocked || result.Canceled || screening.StartsAtMs <= orders.NowMs() {
			status = "BLOCKED"
		} else if taken[seat.ID] != "" {
			status = taken[seat.ID]
			if status == "CONFIRMED" {
				status = "SOLD"
			}
		}
		result.Seats = append(result.Seats, &pb.Seat{Id: seat.ID, Category: seat.Category, PriceMinor: seat.PriceMinor, Status: status})
	}
	return result, nil
}
