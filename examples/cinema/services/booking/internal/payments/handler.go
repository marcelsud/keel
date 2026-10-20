package payments

import (
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/sdk"
)

type PaymentsHandler struct{ Feature *Feature }

func (h *PaymentsHandler) BeginPayment(ctx *sdk.Context, input *pb.BeginPaymentRequest) (*pb.Order, error) {
	return h.Feature.BeginPayment(ctx, input)
}
func (h *PaymentsHandler) Reconcile(ctx *sdk.Context, ref *pb.OrderRef) (*pb.Order, error) {
	if err := h.Feature.Reconcile(ctx, ref); err != nil {
		return nil, err
	}
	return h.Feature.Orders.GetOrder(ctx, ref)
}
