package orders

import (
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/examples/cinema/packages/shared-go"
	"github.com/marcelsud/keel/sdk"
	"strings"
	"time"
)

type State struct {
	Screening cinema.Screening        `json:"screening"`
	Canceled  bool                    `json:"canceled"`
	Orders    map[string]*pb.Order    `json:"orders"`
	Outbox    map[string]cinema.Event `json:"outbox"`
}

func Failure(code int, message string) error { return &sdk.Error{Code: code, Message: message} }

func NowMs() int64 { return time.Now().UnixMilli() }

func Valid(value string) bool { return strings.TrimSpace(value) != "" && len(value) <= 128 }

// mutate keeps a screening's seat claims, orders, and event intents in one SQL transaction.
// ponytail: cap this example at 100 orders per screening; archive orders or partition
// storage before the aggregate approaches NATS's maximum message size.

func Event(state *State, order *pb.Order, kind, suffix string) {
	id := order.Id + ":" + kind + suffix
	state.Outbox[id] = cinema.Event{ID: id, Kind: kind, ScreeningID: order.ScreeningId, OrderID: order.Id, CustomerID: order.CustomerId, Status: order.Status, RefundStatus: order.RefundStatus}
}

func Owned(state *State, ref *pb.OrderRef) (*pb.Order, error) {
	if ref == nil || !Valid(ref.OrderId) || !Valid(ref.CustomerId) {
		return nil, Failure(400, "orderId and customerId are required")
	}
	order := state.Orders[ref.OrderId]
	if order == nil || order.CustomerId != ref.CustomerId {
		return nil, Failure(404, "order not found")
	}
	return order, nil
}

func Occupied(state *State) map[string]string {
	seats := map[string]string{}
	for _, order := range state.Orders {
		if order.Status == "CONFIRMED" || ((order.Status == "HELD" || order.Status == "PAYMENT_PENDING") && order.ExpiresAtMs > float64(NowMs())) {
			for _, seat := range order.Seats {
				seats[seat.Id] = order.Status
			}
		}
	}
	return seats
}

func Cancel(state *State, order *pb.Order) {
	order.Status = "CANCELED"
	for _, ticket := range order.Tickets {
		ticket.Valid = false
	}
	for _, attempt := range order.Attempts {
		if attempt.Status == "APPROVED" {
			attempt.Status, order.RefundStatus = "REFUND_PENDING", "PENDING"
		}
	}
	Event(state, order, "canceled", "")
}
func Expire(state *State) {
	for _, order := range state.Orders {
		if (order.Status == "HELD" || order.Status == "PAYMENT_PENDING") && float64(NowMs()) >= order.ExpiresAtMs {
			order.Status = "EXPIRED"
			Event(state, order, "expired", "")
		}
	}
}
