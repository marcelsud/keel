package availability

import (
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/sdk"
)

type AvailabilityHandler struct{ Feature *Feature }

func (h *AvailabilityHandler) GetAvailability(ctx *sdk.Context, input *pb.ScreeningRef) (*pb.Availability, error) {
	return h.Feature.GetAvailability(ctx, input)
}
