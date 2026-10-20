package reservations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/examples/cinema/packages/shared-go"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/catalog"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/orders"
	"slices"
	"sort"
)

type Feature struct {
	Orders  *orders.Store
	Catalog *catalog.Client
}

func (s Feature) Reserve(ctx context.Context, input *pb.ReserveRequest) (*pb.Order, error) {
	if !orders.Valid(input.CustomerId) || !orders.Valid(input.RequestId) || len(input.SeatIds) == 0 || len(input.SeatIds) > 8 {
		return nil, orders.Failure(400, "customerId, requestId, and 1–8 unique seats are required")
	}
	seats := slices.Clone(input.SeatIds)
	sort.Strings(seats)
	for i, seat := range seats {
		if !orders.Valid(seat) || (i > 0 && seat == seats[i-1]) {
			return nil, orders.Failure(400, "seatIds must be unique and nonempty")
		}
	}
	identity, _ := json.Marshal([]string{input.ScreeningId, input.CustomerId, input.RequestId})
	digest := sha256.Sum256(identity)
	id := hex.EncodeToString(digest[:16])
	screening, err := s.Catalog.GetScreening(ctx, input.ScreeningId)
	if err != nil {
		return nil, err
	}
	state, err := s.Orders.Mutate(ctx, input.ScreeningId, func(state *orders.State) error {
		if existing := state.Orders[id]; existing != nil {
			oldSeats := make([]string, len(existing.Seats))
			for i, seat := range existing.Seats {
				oldSeats[i] = seat.Id
			}
			if existing.CustomerId != input.CustomerId || existing.RequestId != input.RequestId || !slices.Equal(oldSeats, seats) {
				return orders.Failure(409, "requestId already used for different seats")
			}
			return nil
		}
		if state.Canceled || screening.Canceled {
			return orders.Failure(409, "screening canceled")
		}
		if screening.StartsAtMs <= orders.NowMs() {
			return orders.Failure(409, "ticket sales have closed")
		}
		if len(state.Orders) >= 100 {
			return orders.Failure(503, "prototype limit: 100 orders per screening")
		}
		available := map[string]cinema.Seat{}
		for _, seat := range screening.Seats {
			available[seat.ID] = seat
		}
		taken := orders.Occupied(state)
		order := &pb.Order{Id: id, ScreeningId: input.ScreeningId, CustomerId: input.CustomerId, RequestId: input.RequestId, Status: "HELD", Currency: screening.Currency, StartsAtMs: float64(screening.StartsAtMs), CancelCutoffMs: float64(screening.CancelCutoffMs), ExpiresAtMs: float64(min(orders.NowMs()+screening.HoldMs, screening.StartsAtMs)), RefundStatus: "NONE"}
		for _, seatID := range seats {
			seat, exists := available[seatID]
			if !exists || seat.Blocked || taken[seatID] != "" {
				return orders.Failure(409, "seat unavailable: "+seatID)
			}
			order.Seats = append(order.Seats, &pb.Seat{Id: seat.ID, Category: seat.Category, PriceMinor: seat.PriceMinor})
			order.TotalMinor += seat.PriceMinor
		}
		state.Orders[id] = order
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state.Orders[id], nil
}
