package reservations

import (
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/sdk"
)

type ReservationsHandler struct{ Feature *Feature }

func (h *ReservationsHandler) Reserve(ctx *sdk.Context, input *pb.ReserveRequest) (*pb.Order, error) {
	return h.Feature.Reserve(ctx, input)
}
