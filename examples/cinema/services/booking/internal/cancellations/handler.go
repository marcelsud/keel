package cancellations

import (
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/sdk"
)

type CancellationsHandler struct{ Feature *Feature }

func (h *CancellationsHandler) CancelOrder(ctx *sdk.Context, ref *pb.OrderRef) (*pb.Order, error) {
	return h.Feature.CancelOrder(ctx, ref)
}
func (h *CancellationsHandler) CancelScreening(ctx *sdk.Context, input *pb.ScreeningRef) (*pb.Result, error) {
	return h.Feature.CancelScreening(ctx, input)
}
