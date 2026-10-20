package payments

import (
	"context"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	pay "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/payments/v1"
	"github.com/marcelsud/keel/examples/cinema/services/booking/internal/orders"
)

func (s Feature) applyPayment(ctx context.Context, ref *pb.OrderRef, payment *pay.Payment) error {
	_, err := s.Orders.Mutate(ctx, ref.ScreeningId, func(state *orders.State) error {
		order, err := orders.Owned(state, ref)
		if err != nil {
			return err
		}
		if payment.OrderId != order.Id || payment.ScreeningId != order.ScreeningId || payment.CustomerId != order.CustomerId || payment.AmountMinor != order.TotalMinor || payment.Currency != order.Currency {
			return orders.Failure(409, "payment does not match order")
		}
		var attempt *pb.Attempt
		for _, candidate := range order.Attempts {
			if candidate.Id == payment.Id {
				attempt = candidate
			}
		}
		if attempt == nil {
			return orders.Failure(409, "unknown payment attempt")
		}
		if attempt.Status == "REFUNDED" {
			return nil
		}
		switch payment.Status {
		case "APPROVED":
			if attempt.Status == "REFUND_PENDING" {
				return nil
			}
			if order.Status == "CONFIRMED" && order.PaymentId == payment.Id {
				return nil
			}
			if !state.Canceled && order.Status == "PAYMENT_PENDING" && order.PaymentId == payment.Id && float64(orders.NowMs()) < order.ExpiresAtMs {
				order.Status, attempt.Status = "CONFIRMED", "APPROVED"
				for _, seat := range order.Seats {
					order.Tickets = append(order.Tickets, &pb.Ticket{Id: order.Id + "-" + seat.Id, SeatId: seat.Id, Valid: true})
				}
				orders.Event(state, order, "confirmed", "")
			} else {
				attempt.Status, order.RefundStatus = "REFUND_PENDING", "PENDING"
			}
		case "DECLINED":
			if attempt.Status != "PENDING" {
				return nil
			}
			attempt.Status = "DECLINED"
			if order.Status == "PAYMENT_PENDING" && order.PaymentId == payment.Id {
				order.Status = "HELD"
			}
		case "REFUNDED":
			attempt.Status = "REFUNDED"
			if order.Status == "CONFIRMED" && order.PaymentId == payment.Id {
				orders.Cancel(state, order)
			}
			order.RefundStatus = "REFUNDED"
			for _, other := range order.Attempts {
				if other.Status == "REFUND_PENDING" {
					order.RefundStatus = "PENDING"
				}
			}
			orders.Event(state, order, "refunded", ":"+payment.Id)
		}
		return nil
	})
	return err
}
