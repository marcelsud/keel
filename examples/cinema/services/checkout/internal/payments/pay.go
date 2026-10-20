package payments

import (
	"context"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	pay "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/payments/v1"
)

type Service struct {
	Booking  *pb.BookingServiceClient
	Payments *pay.PaymentsServiceClient
}

func (s *Service) Pay(ctx context.Context, input Request) (*pb.Order, error) {

	if input.Mode == "" {
		input.Mode = "approve"
	}
	ref := &pb.OrderRef{ScreeningId: input.ScreeningID, OrderId: input.OrderID, CustomerId: input.CustomerID}
	order, err := s.Booking.BeginPayment(ctx, &pb.BeginPaymentRequest{Order: ref, RequestId: input.RequestID, Mode: input.Mode, DelayMs: input.DelayMs, RefundFailures: input.RefundFailures})
	if err != nil {
		return nil, err
	}
	if _, err := s.Payments.Charge(ctx, &pay.ChargeRequest{PaymentId: order.PaymentId, ScreeningId: order.ScreeningId, OrderId: order.Id, CustomerId: order.CustomerId}); err != nil {
		return nil, err
	}
	return s.Booking.Reconcile(ctx, ref)
}
