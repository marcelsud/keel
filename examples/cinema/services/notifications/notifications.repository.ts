import { type SQL, type SQLTransaction } from "@marcelsud/keel.ts";
import { type BookingEvent } from "../../packages/shared-ts/index.js";
import { type NotificationState } from "./notifications.model.js";
type DeliveryRow = BookingEvent & {
  attempts: number;
  delivered: number;
  receiptId: string;
};

export class NotificationsRepository {
  constructor(private readonly sql: SQL) {}
  read(orderId: string): Promise<NotificationState> {
    return this.readFrom(this.sql, orderId);
  }

  private async readFrom(
    connection: SQL | SQLTransaction,
    orderId: string,
  ): Promise<NotificationState> {
    const { rows } = await connection.query<DeliveryRow>(
      `SELECT id,order_id AS orderId,json_quote(customer_id) AS customerId,screening_id AS screeningId,
      kind,booking_status AS status,refund_status AS refundStatus,attempts,delivered,receipt_id AS receiptId FROM deliveries WHERE order_id=? ORDER BY id`,
      [orderId],
    );
    return {
      deliveries: Object.fromEntries(
        rows.map(({ attempts, delivered, receiptId, ...event }) => [
          event.id,
          {
            event: { ...event, customerId: JSON.parse(event.customerId) },
            attempts,
            delivered: Boolean(delivered),
            receiptId,
          },
        ]),
      ),
    };
  }

  update<T>(
    orderId: string,
    change: (state: NotificationState) => T,
  ): Promise<T> {
    return this.sql.transaction(async (tx) => {
      const state = await this.readFrom(tx, orderId);
      const before = new Map(
        Object.entries(state.deliveries).map(([id, item]) => [
          id,
          JSON.stringify(item),
        ]),
      );
      const result = change(state);
      for (const item of Object.values(state.deliveries)) {
        if (before.get(item.event.id) === JSON.stringify(item)) continue;
        const event = item.event;
        await tx.query(
          `INSERT INTO deliveries(id,order_id,customer_id,screening_id,kind,booking_status,refund_status,attempts,delivered,receipt_id)
          VALUES (?,?,json_extract(?,'$'),?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET attempts=excluded.attempts,delivered=excluded.delivered`,
          [
            event.id,
            event.orderId,
            JSON.stringify(event.customerId),
            event.screeningId,
            event.kind,
            event.status,
            event.refundStatus,
            item.attempts,
            item.delivered,
            item.receiptId,
          ],
        );
      }
      return result;
    });
  }
}
