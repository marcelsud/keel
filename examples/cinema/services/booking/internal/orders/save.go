package orders

import (
	"context"
	"encoding/json"
	pb "github.com/marcelsud/keel/examples/cinema/contracts/generated/go/cinema/booking/v1"
	"github.com/marcelsud/keel/sdk"
)

func saveOrder(ctx context.Context, tx *sdk.SQLTx, order *pb.Order) error {
	// Opaque IDs may contain NUL. SQLite decodes the JSON inside the engine;
	// its native wire protocol only sees escaped, NUL-free parameters.
	customer, _ := json.Marshal(order.CustomerId)
	request, _ := json.Marshal(order.RequestId)
	_, err := tx.Query(ctx, `INSERT INTO orders(id,screening_id,customer_id,request_id,status,total_minor,currency,expires_at_ms,starts_at_ms,cancel_cutoff_ms,payment_id,refund_status)
 VALUES (?,?,json_extract(?,'$'),json_extract(?,'$'),?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,payment_id=excluded.payment_id,refund_status=excluded.refund_status`,
		order.Id, order.ScreeningId, string(customer), string(request), order.Status, order.TotalMinor, order.Currency, order.ExpiresAtMs, order.StartsAtMs, order.CancelCutoffMs, order.PaymentId, order.RefundStatus)
	if err != nil {
		return err
	}
	children := []struct {
		deleteSQL, insertSQL string
		records              any
	}{
		{"DELETE FROM order_seats WHERE order_id=?", `INSERT INTO order_seats(order_id,id,category,price_minor,status) SELECT ?,json_extract(value,'$.id'),json_extract(value,'$.category'),json_extract(value,'$.price_minor'),COALESCE(json_extract(value,'$.status'),'') FROM json_each(?)`, order.Seats},
		{"DELETE FROM tickets WHERE order_id=?", `INSERT INTO tickets(id,order_id,seat_id,valid) SELECT json_extract(value,'$.id'),?,json_extract(value,'$.seat_id'),COALESCE(json_extract(value,'$.valid'),0) FROM json_each(?)`, order.Tickets},
		{"DELETE FROM payment_attempts WHERE order_id=?", `INSERT INTO payment_attempts(id,order_id,position,request_id,mode,delay_ms,refund_failures,status) SELECT json_extract(value,'$.id'),?,CAST(key AS INTEGER),json_extract(value,'$.request_id'),json_extract(value,'$.mode'),COALESCE(json_extract(value,'$.delay_ms'),0),COALESCE(json_extract(value,'$.refund_failures'),0),json_extract(value,'$.status') FROM json_each(?)`, order.Attempts},
	}
	for _, child := range children {
		if _, err := tx.Query(ctx, child.deleteSQL, order.Id); err != nil {
			return err
		}
		data, err := json.Marshal(child.records)
		if err != nil {
			return err
		}
		if string(data) == "null" {
			data = []byte("[]")
		}
		if _, err := tx.Query(ctx, child.insertSQL, order.Id, string(data)); err != nil {
			return err
		}
	}
	return nil
}

func saveOutbox(ctx context.Context, tx *sdk.SQLTx, id string, state *State) error {
	if _, err := tx.Query(ctx, "DELETE FROM booking_outbox WHERE screening_id=?", id); err != nil {
		return err
	}
	data, err := json.Marshal(state.Outbox)
	if err != nil {
		return err
	}
	_, err = tx.Query(ctx, `INSERT INTO booking_outbox(id,screening_id,order_id,customer_id,kind,status,refund_status)
 SELECT key,?,json_extract(value,'$.orderId'),json_extract(value,'$.customerId'),json_extract(value,'$.kind'),json_extract(value,'$.status'),json_extract(value,'$.refundStatus') FROM json_each(?)`, id, string(data))
	return err
}
