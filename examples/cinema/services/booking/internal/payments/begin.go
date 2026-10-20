package payments

import (
	"context"
	"fmt"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	pay "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/payments/v1"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/orders"
	"slices"
)

type Feature struct {
	Orders   *orders.Store
	Provider *pay.PaymentsServiceClient
}

func (s Feature) BeginPayment(ctx context.Context, input *pb.BeginPaymentRequest) (*pb.Order, error) {
	if !orders.Valid(input.RequestId) || !slices.Contains([]string{"approve", "decline", "lost_callback", "duplicate_callback"}, input.Mode) || input.DelayMs < 0 || input.DelayMs > 60_000 || input.RefundFailures < 0 || input.RefundFailures > 5 {
		return nil, orders.Failure(400, "invalid payment request or simulation settings")
	}
	state, err := s.Orders.Mutate(ctx, input.Order.GetScreeningId(), func(state *orders.State) error {
		order, err := orders.Owned(state, input.Order)
		if err != nil {
			return err
		}
		for _, attempt := range order.Attempts {
			if attempt.RequestId == input.RequestId {
				if attempt.Mode != input.Mode || attempt.DelayMs != input.DelayMs || attempt.RefundFailures != input.RefundFailures {
					return orders.Failure(409, "requestId already used with different payment settings")
				}
				return nil
			}
		}
		if state.Canceled || order.Status != "HELD" {
			return orders.Failure(409, "order is not available for payment")
		}
		if len(order.Attempts) >= 5 {
			return orders.Failure(409, "prototype limit: five payment attempts per order")
		}
		id := fmt.Sprintf("%s-%d", order.Id, len(order.Attempts)+1)
		order.Attempts = append(order.Attempts, &pb.Attempt{Id: id, RequestId: input.RequestId, Mode: input.Mode, DelayMs: input.DelayMs, RefundFailures: input.RefundFailures, Status: "PENDING"})
		order.PaymentId, order.Status = id, "PAYMENT_PENDING"
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state.Orders[input.Order.OrderId], nil
}
