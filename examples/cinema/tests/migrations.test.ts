import { test } from "bun:test";
import { Database } from "bun:sqlite";
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const event = {
  id: "order-1:confirmed",
  screeningId: "show-1",
  orderId: "order-1",
  customerId: "ada",
  kind: "confirmed",
  status: "CONFIRMED",
  refundStatus: "NONE",
};
const cases = [
  {
    service: "catalog",
    first: "0001_catalog.sql",
    next: "0002_screenings.sql",
    key: "schedule",
    document: {
      screenings: {
        "show-1": {
          id: "show-1",
          cinemaId: "downtown",
          auditoriumId: "hall-1",
          movieId: "orbit",
          startsAtMs: 100000,
          endsAtMs: 200000,
          basePriceMinor: 1200,
          currency: "USD",
          holdMs: 5000,
          cancelCutoffMs: 1000,
          canceled: false,
          seats: [
            {
              id: "B1",
              category: "standard",
              priceMinor: 1200,
              blocked: false,
            },
          ],
        },
      },
    },
    query: "SELECT id,cinema_id,base_price_minor FROM screenings",
    expected: [{ id: "show-1", cinema_id: "downtown", base_price_minor: 1200 }],
  },
  {
    service: "booking",
    first: "0001_state.sql",
    next: "0002_orders.sql",
    key: "show-1",
    document: {
      canceled: false,
      orders: {
        "order-1": {
          id: "order-1",
          screening_id: "show-1",
          customer_id: "ada",
          request_id: "hold-1",
          status: "CANCELED",
          total_minor: 1200,
          currency: "USD",
          expires_at_ms: 10000,
          starts_at_ms: 20000,
          refund_status: "NONE",
          seats: [{ id: "B1", category: "standard", price_minor: 1200 }],
          tickets: [{ id: "ticket-1", seat_id: "B1" }],
          attempts: [
            {
              id: "attempt-1",
              request_id: "pay-1",
              mode: "approve",
              status: "DECLINED",
            },
          ],
        },
      },
      outbox: { [event.id]: event },
    },
    query: "SELECT id,customer_id,status FROM orders",
    expected: [{ id: "order-1", customer_id: "ada", status: "CANCELED" }],
  },
  {
    service: "payments",
    first: "0001_state.sql",
    next: "0002_payments.sql",
    key: "provider",
    document: {
      payments: {
        "payment-1": {
          id: "payment-1",
          screeningId: "show-1",
          orderId: "order-1",
          customerId: "ada",
          status: "APPROVED",
          amountMinor: 1200,
          currency: "USD",
          mode: "approve",
          dueAtMs: 10000,
          chargeCount: 1,
          refundCount: 0,
          refundFailuresRemaining: 0,
        },
      },
      outbox: {
        "payment-1:APPROVED": {
          id: "payment-1:APPROVED",
          paymentId: "payment-1",
          screeningId: "show-1",
          orderId: "order-1",
          customerId: "ada",
          copies: 2,
        },
      },
    },
    query: "SELECT id,amount_minor,charge_count FROM payments",
    expected: [{ id: "payment-1", amount_minor: 1200, charge_count: 1 }],
  },
  {
    service: "notifications",
    first: "0001_state.sql",
    next: "0002_deliveries.sql",
    key: "order-1",
    document: {
      deliveries: {
        [event.id]: {
          event,
          attempts: 2,
          delivered: true,
          receiptId: "receipt-1",
        },
      },
    },
    query: "SELECT id,attempts,delivered FROM deliveries",
    expected: [{ id: event.id, attempts: 2, delivered: 1 }],
  },
];

for (const fixture of cases) {
  test(`${fixture.service} migrates persisted legacy records into entity tables`, async () => {
    const directory = await mkdtemp(join(tmpdir(), "keel-entity-migration-"));
    const db = new Database(join(directory, "service.sqlite"));
    try {
      db.exec("PRAGMA foreign_keys=ON");
      const migrations = new URL(
        `../services/${fixture.service}/migrations/`,
        import.meta.url,
      );
      db.exec(await readFile(new URL(fixture.first, migrations), "utf8"));
      db.query("INSERT INTO state(key,document) VALUES (?,?)").run(
        fixture.key,
        JSON.stringify(fixture.document),
      );
      db.exec(await readFile(new URL(fixture.next, migrations), "utf8"));
      assert.deepEqual(db.query(fixture.query).all(), fixture.expected);
      assert.deepEqual(
        db.query("SELECT name FROM sqlite_master WHERE name='state'").all(),
        [],
      );
      if (fixture.service === "booking") {
        assert.deepEqual(
          db.query("SELECT order_id,id FROM order_seats").all(),
          [{ order_id: "order-1", id: "B1" }],
        );
        assert.deepEqual(db.query("SELECT valid FROM tickets").all(), [
          { valid: 0 },
        ]);
        assert.deepEqual(
          db
            .query("SELECT delay_ms,refund_failures FROM payment_attempts")
            .all(),
          [{ delay_ms: 0, refund_failures: 0 }],
        );
        assert.deepEqual(db.query("SELECT id FROM booking_outbox").all(), [
          { id: event.id },
        ]);
      }
      if (fixture.service === "payments")
        assert.deepEqual(db.query("SELECT copies FROM payment_outbox").all(), [
          { copies: 2 },
        ]);
    } finally {
      db.close();
      await rm(directory, { recursive: true, force: true });
    }
  });
}
