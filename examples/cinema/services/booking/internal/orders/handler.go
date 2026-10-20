package orders

import (
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/sdk"
)

type OrdersHandler struct{ Store *Store }

func (h *OrdersHandler) GetOrder(ctx *sdk.Context, ref *pb.OrderRef) (*pb.Order, error) {
	return h.Store.GetOrder(ctx, ref)
}
