import { type BookingEvent } from "../../packages/shared-ts/index.js";
import { NotificationsRepository } from "./notifications.repository.js";
import {
  validateBookingEvent,
  type ReceiptQueryDto,
} from "./notifications.dto.js";
export class NotificationsService {
  constructor(
    private readonly repository: NotificationsRepository,
    private readonly failures: number,
  ) {}
  async deliver(event: BookingEvent): Promise<void> {
    validateBookingEvent(event);
    const delivery = await this.repository.update(event.orderId, (state) => {
      const delivery = (state.deliveries[event.id] ??= {
        event,
        attempts: 0,
        delivered: false,
        receiptId: `receipt-${event.id}`,
      });
      if (delivery.delivered) return delivery;
      delivery.attempts++;
      if (delivery.attempts > this.failures) delivery.delivered = true;
      return delivery;
    });
    if (!delivery.delivered)
      throw new Error("simulated notification failure; purchase remains valid");
  }
  async receipts(input: ReceiptQueryDto) {
    const state = await this.repository.read(input.orderId);
    return Object.values(state.deliveries).filter(
      (delivery) =>
        delivery.delivered &&
        delivery.event.orderId === input.orderId &&
        delivery.event.customerId === input.customerId,
    );
  }
}
