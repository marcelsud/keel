package payments

import (
	"context"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	pay "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/payments/v1"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/orders"
)

func (s Feature) Reconcile(ctx context.Context, ref *pb.OrderRef) error {
	state, err := s.Orders.Mutate(ctx, ref.ScreeningId, func(state *orders.State) error { _, err := orders.Owned(state, ref); return err })
	if err != nil {
		return err
	}
	client := s.Provider
	for _, attempt := range state.Orders[ref.OrderId].Attempts {
		if attempt.Status == "DECLINED" || attempt.Status == "REFUNDED" || (attempt.Status == "APPROVED" && state.Orders[ref.OrderId].Status == "CONFIRMED") {
			continue
		}
		var payment *pay.Payment
		if attempt.Status == "REFUND_PENDING" {
			payment, err = client.Refund(ctx, &pay.PaymentRef{PaymentId: attempt.Id})
		} else {
			payment, err = client.Charge(ctx, &pay.ChargeRequest{PaymentId: attempt.Id, ScreeningId: ref.ScreeningId, OrderId: ref.OrderId, CustomerId: ref.CustomerId})
		}
		if err != nil {
			return err
		}
		if err := s.applyPayment(ctx, ref, payment); err != nil {
			return err
		}
	}
	return nil
}
