-- +goose Up
CREATE TABLE deliveries (
 id TEXT PRIMARY KEY, order_id TEXT NOT NULL, customer_id TEXT NOT NULL, screening_id TEXT NOT NULL,
 kind TEXT NOT NULL, booking_status TEXT NOT NULL, refund_status TEXT NOT NULL,
 attempts INTEGER NOT NULL, delivered INTEGER NOT NULL CHECK(delivered IN (0,1)), receipt_id TEXT NOT NULL
);
CREATE INDEX deliveries_customer_order ON deliveries(customer_id,order_id);
INSERT INTO deliveries SELECT j.key,json_extract(j.value,'$.event.orderId'),json_extract(j.value,'$.event.customerId'),json_extract(j.value,'$.event.screeningId'),
json_extract(j.value,'$.event.kind'),COALESCE(json_extract(j.value,'$.event.status'),''),COALESCE(json_extract(j.value,'$.event.refundStatus'),''),
json_extract(j.value,'$.attempts'),json_extract(j.value,'$.delivered'),json_extract(j.value,'$.receiptId')
FROM state s,json_each(s.document,'$.deliveries') j;
DROP TABLE state;
