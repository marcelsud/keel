import { type SQL, type SQLTransaction } from "@marcelsud/keel.ts";
import { own, fail } from "../../packages/shared-ts/index.js";
import {
  type State,
  type Payment,
  type PaymentEvent,
} from "./payments.model.js";

export class PaymentsRepository {
  constructor(private readonly sql: SQL) {}
  read(): Promise<State> {
    return this.readFrom(this.sql);
  }

  private async readFrom(connection: SQL | SQLTransaction): Promise<State> {
    const payments = (
      await connection.query<Payment>(`SELECT id,screening_id AS screeningId,order_id AS orderId,json_quote(customer_id) AS customerId,status,
      amount_minor AS amountMinor,currency,mode,due_at_ms AS dueAtMs,charge_count AS chargeCount,refund_count AS refundCount,
      refund_failures_remaining AS refundFailuresRemaining FROM payments ORDER BY id`)
    ).rows;
    const outbox = (
      await connection.query<PaymentEvent>(`SELECT id,payment_id AS paymentId,screening_id AS screeningId,order_id AS orderId,
      json_quote(customer_id) AS customerId,copies FROM payment_outbox ORDER BY id`)
    ).rows;
    return {
      payments: Object.fromEntries(
        payments.map((item) => [
          item.id,
          { ...item, customerId: JSON.parse(item.customerId) },
        ]),
      ),
      outbox: Object.fromEntries(
        outbox.map((item) => [
          item.id,
          { ...item, customerId: JSON.parse(item.customerId) },
        ]),
      ),
    };
  }

  async get(id: string): Promise<Payment> {
    return (
      own((await this.read()).payments, id) ?? fail(404, "payment not found")
    );
  }

  update<T>(change: (state: State) => T): Promise<T> {
    return this.sql.transaction(async (tx) => {
      const state = await this.readFrom(tx);
      const before = new Map(
        Object.entries(state.payments).map(([id, item]) => [
          id,
          JSON.stringify(item),
        ]),
      );
      const outbox = new Map(
        Object.entries(state.outbox).map(([id, item]) => [
          id,
          JSON.stringify(item),
        ]),
      );
      const result = change(state);
      for (const payment of Object.values(state.payments)) {
        if (before.get(payment.id) === JSON.stringify(payment)) continue;
        await tx.query(
          `INSERT INTO payments(id,screening_id,order_id,customer_id,status,amount_minor,currency,mode,due_at_ms,charge_count,refund_count,refund_failures_remaining)
          VALUES (?,?,?,json_extract(?,'$'),?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,refund_count=excluded.refund_count,refund_failures_remaining=excluded.refund_failures_remaining`,
          [
            payment.id,
            payment.screeningId,
            payment.orderId,
            JSON.stringify(payment.customerId),
            payment.status,
            payment.amountMinor,
            payment.currency,
            payment.mode,
            payment.dueAtMs,
            payment.chargeCount,
            payment.refundCount,
            payment.refundFailuresRemaining,
          ],
        );
      }
      for (const id of outbox.keys())
        if (!Object.hasOwn(state.outbox, id))
          await tx.query("DELETE FROM payment_outbox WHERE id=?", [id]);
      for (const item of Object.values(state.outbox)) {
        if (outbox.get(item.id) === JSON.stringify(item)) continue;
        await tx.query(
          `INSERT INTO payment_outbox(id,payment_id,screening_id,order_id,customer_id,copies) VALUES (?,?,?,?,json_extract(?,'$'),?)
          ON CONFLICT(id) DO UPDATE SET copies=excluded.copies`,
          [
            item.id,
            item.paymentId,
            item.screeningId,
            item.orderId,
            JSON.stringify(item.customerId),
            item.copies,
          ],
        );
      }
      return result;
    });
  }
}
