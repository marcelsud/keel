-- +goose Up
CREATE TABLE screenings (id TEXT PRIMARY KEY, canceled INTEGER NOT NULL CHECK(canceled IN (0,1)));
CREATE TABLE orders (
 id TEXT PRIMARY KEY, screening_id TEXT NOT NULL REFERENCES screenings(id), customer_id TEXT NOT NULL, request_id TEXT NOT NULL,
 status TEXT NOT NULL, total_minor INTEGER NOT NULL, currency TEXT NOT NULL,
 expires_at_ms REAL NOT NULL, starts_at_ms REAL NOT NULL, cancel_cutoff_ms REAL NOT NULL,
 payment_id TEXT NOT NULL, refund_status TEXT NOT NULL,
 UNIQUE(screening_id,customer_id,request_id)
);
CREATE TABLE order_seats (order_id TEXT NOT NULL REFERENCES orders(id), id TEXT NOT NULL, category TEXT NOT NULL, price_minor INTEGER NOT NULL, status TEXT NOT NULL, PRIMARY KEY(order_id,id));
CREATE TABLE tickets (id TEXT PRIMARY KEY, order_id TEXT NOT NULL REFERENCES orders(id), seat_id TEXT NOT NULL, valid INTEGER NOT NULL CHECK(valid IN (0,1)));
CREATE TABLE payment_attempts (
 id TEXT PRIMARY KEY, order_id TEXT NOT NULL REFERENCES orders(id), position INTEGER NOT NULL,
 request_id TEXT NOT NULL, mode TEXT NOT NULL, delay_ms INTEGER NOT NULL, refund_failures INTEGER NOT NULL,
 status TEXT NOT NULL, UNIQUE(order_id,request_id)
);
CREATE TABLE booking_outbox (id TEXT PRIMARY KEY, screening_id TEXT NOT NULL REFERENCES screenings(id), order_id TEXT NOT NULL REFERENCES orders(id), customer_id TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, refund_status TEXT NOT NULL);
INSERT INTO screenings SELECT key,COALESCE(json_extract(document,'$.canceled'),0) FROM state;
INSERT INTO orders SELECT j.key,s.key,json_extract(j.value,'$.customer_id'),json_extract(j.value,'$.request_id'),
json_extract(j.value,'$.status'),json_extract(j.value,'$.total_minor'),json_extract(j.value,'$.currency'),
json_extract(j.value,'$.expires_at_ms'),json_extract(j.value,'$.starts_at_ms'),COALESCE(json_extract(j.value,'$.cancel_cutoff_ms'),0),
COALESCE(json_extract(j.value,'$.payment_id'),''),COALESCE(json_extract(j.value,'$.refund_status'),'NONE')
FROM state s,json_each(s.document,'$.orders') j;
INSERT INTO order_seats SELECT o.key,json_extract(j.value,'$.id'),json_extract(j.value,'$.category'),json_extract(j.value,'$.price_minor'),COALESCE(json_extract(j.value,'$.status'),'')
FROM state s,json_each(s.document,'$.orders') o,json_each(o.value,'$.seats') j;
INSERT INTO tickets SELECT json_extract(j.value,'$.id'),o.key,json_extract(j.value,'$.seat_id'),COALESCE(json_extract(j.value,'$.valid'),0)
FROM state s,json_each(s.document,'$.orders') o,json_each(o.value,'$.tickets') j;
INSERT INTO payment_attempts SELECT json_extract(j.value,'$.id'),o.key,CAST(j.key AS INTEGER),json_extract(j.value,'$.request_id'),
json_extract(j.value,'$.mode'),COALESCE(json_extract(j.value,'$.delay_ms'),0),COALESCE(json_extract(j.value,'$.refund_failures'),0),json_extract(j.value,'$.status')
FROM state s,json_each(s.document,'$.orders') o,json_each(o.value,'$.attempts') j;
INSERT INTO booking_outbox SELECT j.key,s.key,json_extract(j.value,'$.orderId'),json_extract(j.value,'$.customerId'),
json_extract(j.value,'$.kind'),COALESCE(json_extract(j.value,'$.status'),''),COALESCE(json_extract(j.value,'$.refundStatus'),'')
FROM state s,json_each(s.document,'$.outbox') j;
DROP TABLE state;
