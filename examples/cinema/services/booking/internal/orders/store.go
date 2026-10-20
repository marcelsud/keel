package orders

import (
	"context"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/catalog"
)

type Store struct {
	Repository *Repository
	Catalog    *catalog.Client
}

func (s *Store) Mutate(ctx context.Context, id string, update func(*State) error) (*State, error) {
	if !Valid(id) {
		return nil, Failure(400, "screeningId is required")
	}
	screening, err := s.Catalog.GetScreening(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.Repository.Update(ctx, id, screening, update)
}
func (s *Store) GetOrder(ctx context.Context, ref *pb.OrderRef) (*pb.Order, error) {
	state, err := s.Mutate(ctx, ref.GetScreeningId(), func(state *State) error { _, err := Owned(state, ref); return err })
	if err != nil {
		return nil, err
	}
	return state.Orders[ref.OrderId], nil
}
