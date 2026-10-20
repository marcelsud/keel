package orders

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/examples/cinema/packages/shared-go"
	"github.com/marcelsud/keel/sdk"
)

type Repository struct{ SQL *sdk.SQL }

func (r *Repository) Exists(ctx context.Context, id string) (bool, error) {
	result, err := r.SQL.Query(ctx, "SELECT id FROM screenings WHERE id=?", id)
	return len(result.Rows) > 0, err
}

// Update hydrates an aggregate from tables for this transaction only. It is never cached.
func (r *Repository) Update(ctx context.Context, id string, screening cinema.Screening, change func(*State) error) (*State, error) {
	var result *State
	err := r.SQL.Transaction(ctx, func(tx *sdk.SQLTx) error {
		state, exists, err := loadState(ctx, tx, id, screening)
		if err != nil {
			return err
		}
		canceled := state.Canceled
		before := map[string][]byte{}
		for id, order := range state.Orders {
			before[id], _ = json.Marshal(order)
		}
		oldOutbox, _ := json.Marshal(state.Outbox)
		Expire(state)
		if err := change(state); err != nil {
			return err
		}
		if !exists || canceled != state.Canceled {
			if _, err := tx.Query(ctx, "INSERT INTO screenings(id,canceled) VALUES (?,?) ON CONFLICT(id) DO UPDATE SET canceled=excluded.canceled", id, state.Canceled); err != nil {
				return err
			}
		}
		for id, order := range state.Orders {
			after, _ := json.Marshal(order)
			if !bytes.Equal(before[id], after) {
				if err := saveOrder(ctx, tx, order); err != nil {
					return err
				}
			}
		}
		newOutbox, _ := json.Marshal(state.Outbox)
		if !bytes.Equal(oldOutbox, newOutbox) {
			if err := saveOutbox(ctx, tx, id, state); err != nil {
				return err
			}
		}
		result = state
		return nil
	})
	return result, err
}

func loadState(ctx context.Context, tx *sdk.SQLTx, id string, screening cinema.Screening) (*State, bool, error) {
	state := &State{Screening: screening, Canceled: screening.Canceled, Orders: map[string]*pb.Order{}, Outbox: map[string]cinema.Event{}}
	header, err := tx.Query(ctx, "SELECT canceled FROM screenings WHERE id=?", id)
	if err != nil {
		return nil, false, err
	}
	exists := len(header.Rows) > 0
	if !exists {
		return state, false, nil
	}
	state.Canceled = fmt.Sprint(header.Rows[0]["canceled"]) == "1"
	rows, err := tx.Query(ctx, "SELECT json_object('id',o.id,'screening_id',o.screening_id,'customer_id',o.customer_id,'request_id',o.request_id,'status',o.status,'total_minor',o.total_minor,'currency',o.currency,'expires_at_ms',o.expires_at_ms,'starts_at_ms',o.starts_at_ms,'cancel_cutoff_ms',o.cancel_cutoff_ms,'payment_id',o.payment_id,'refund_status',o.refund_status) AS document FROM orders o WHERE o.screening_id=? ORDER BY o.id", id)
	if err != nil {
		return nil, false, err
	}
	for _, row := range rows.Rows {
		order := &pb.Order{}
		if err := json.Unmarshal([]byte(row["document"].(string)), order); err != nil {
			return nil, false, err
		}
		state.Orders[order.Id] = order
	}
	children := []struct {
		query string
		load  func(*pb.Order, []byte) error
	}{
		{"SELECT c.order_id,json_object('id',c.id,'category',c.category,'price_minor',c.price_minor,'status',c.status) AS document FROM order_seats c JOIN orders o ON o.id=c.order_id WHERE o.screening_id=? ORDER BY c.id", func(order *pb.Order, document []byte) error {
			seat := &pb.Seat{}
			if err := json.Unmarshal(document, seat); err != nil {
				return err
			}
			order.Seats = append(order.Seats, seat)
			return nil
		}},
		{"SELECT c.order_id,json_object('id',c.id,'seat_id',c.seat_id,'valid',json(CASE c.valid WHEN 1 THEN 'true' ELSE 'false' END)) AS document FROM tickets c JOIN orders o ON o.id=c.order_id WHERE o.screening_id=? ORDER BY c.id", func(order *pb.Order, document []byte) error {
			ticket := &pb.Ticket{}
			if err := json.Unmarshal(document, ticket); err != nil {
				return err
			}
			order.Tickets = append(order.Tickets, ticket)
			return nil
		}},
		{"SELECT c.order_id,json_object('id',c.id,'request_id',c.request_id,'mode',c.mode,'delay_ms',c.delay_ms,'refund_failures',c.refund_failures,'status',c.status) AS document FROM payment_attempts c JOIN orders o ON o.id=c.order_id WHERE o.screening_id=? ORDER BY c.position", func(order *pb.Order, document []byte) error {
			attempt := &pb.Attempt{}
			if err := json.Unmarshal(document, attempt); err != nil {
				return err
			}
			order.Attempts = append(order.Attempts, attempt)
			return nil
		}},
	}
	for _, child := range children {
		rows, err := tx.Query(ctx, child.query, id)
		if err != nil {
			return nil, false, err
		}
		for _, row := range rows.Rows {
			order := state.Orders[row["order_id"].(string)]
			if order == nil {
				return nil, false, fmt.Errorf("child row references a missing order")
			}
			if err := child.load(order, []byte(row["document"].(string))); err != nil {
				return nil, false, err
			}
		}
	}
	rows, err = tx.Query(ctx, "SELECT json_object('id',e.id,'screeningId',e.screening_id,'orderId',e.order_id,'customerId',e.customer_id,'kind',e.kind,'status',e.status,'refundStatus',e.refund_status) AS document FROM booking_outbox e WHERE e.screening_id=? ORDER BY e.id", id)
	if err != nil {
		return nil, false, err
	}
	for _, row := range rows.Rows {
		var event cinema.Event
		if err := json.Unmarshal([]byte(row["document"].(string)), &event); err != nil {
			return nil, false, err
		}
		state.Outbox[event.ID] = event
	}
	return state, true, nil
}
