-- +goose Up
CREATE TABLE payments (
 id TEXT PRIMARY KEY, screening_id TEXT NOT NULL, order_id TEXT NOT NULL, customer_id TEXT NOT NULL,
 status TEXT NOT NULL, amount_minor INTEGER NOT NULL, currency TEXT NOT NULL, mode TEXT NOT NULL,
 due_at_ms REAL NOT NULL, charge_count INTEGER NOT NULL, refund_count INTEGER NOT NULL,
 refund_failures_remaining INTEGER NOT NULL
);
CREATE INDEX payments_order ON payments(order_id);
CREATE TABLE payment_outbox (
 id TEXT PRIMARY KEY, payment_id TEXT NOT NULL REFERENCES payments(id), screening_id TEXT NOT NULL,
 order_id TEXT NOT NULL, customer_id TEXT NOT NULL, copies INTEGER NOT NULL
);
INSERT INTO payments SELECT j.key,json_extract(j.value,'$.screeningId'),json_extract(j.value,'$.orderId'),json_extract(j.value,'$.customerId'),
json_extract(j.value,'$.status'),json_extract(j.value,'$.amountMinor'),json_extract(j.value,'$.currency'),json_extract(j.value,'$.mode'),
json_extract(j.value,'$.dueAtMs'),json_extract(j.value,'$.chargeCount'),json_extract(j.value,'$.refundCount'),json_extract(j.value,'$.refundFailuresRemaining')
FROM state s,json_each(s.document,'$.payments') j WHERE s.key='provider';
INSERT INTO payment_outbox SELECT j.key,json_extract(j.value,'$.paymentId'),json_extract(j.value,'$.screeningId'),json_extract(j.value,'$.orderId'),json_extract(j.value,'$.customerId'),json_extract(j.value,'$.copies')
FROM state s,json_each(s.document,'$.outbox') j WHERE s.key='provider';
DROP TABLE state;
