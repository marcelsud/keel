import { type ServiceClient } from "@marcelsud/keel.ts";
import { type BookingServiceContract } from "../../contracts/generated/typescript/cinema/booking/v1/booking_keel.js";
import { fail, identifier } from "../../packages/shared-ts/index.js";
import { type Payment, queuePaymentEvent } from "./payments.model.js";
import { type ChargePaymentDto } from "./payments.dto.js";
import { PaymentsRepository } from "./payments.repository.js";
export class ChargePaymentService {
  constructor(
    private readonly repository: PaymentsRepository,
    private readonly booking: ServiceClient<
      typeof BookingServiceContract.service
    >,
  ) {}
  async charge(input: ChargePaymentDto): Promise<Payment> {
    identifier(input.paymentId, "paymentId");
    const existing = (await this.repository.read()).payments[input.paymentId];
    if (existing) {
      if (
        existing.orderId !== input.orderId ||
        existing.screeningId !== input.screeningId ||
        existing.customerId !== input.customerId
      )
        fail(409, "paymentId already belongs to another order");
      return existing;
    }
    const order = await this.booking.getOrder({
      screeningId: input.screeningId,
      orderId: input.orderId,
      customerId: input.customerId,
    });
    const attempt =
      order.attempts.find((attempt) => attempt.id === input.paymentId) ??
      fail(409, "payment attempt was not authorized by Booking");
    return this.repository.update((state) => {
      if (state.payments[input.paymentId])
        return state.payments[input.paymentId]!;
      if (Object.keys(state.payments).length >= 500)
        fail(503, "prototype limit: 500 payment attempts");
      const authorized =
        order.status === "PAYMENT_PENDING" &&
        order.paymentId === input.paymentId &&
        order.expiresAtMs > Date.now();
      const payment: Payment = {
        id: input.paymentId,
        screeningId: input.screeningId,
        orderId: input.orderId,
        customerId: input.customerId,
        amountMinor: order.totalMinor,
        currency: order.currency,
        status: authorized ? "PENDING" : "DECLINED",
        mode: attempt.mode,
        dueAtMs: Date.now() + attempt.delayMs,
        chargeCount: authorized ? 1 : 0,
        refundCount: 0,
        refundFailuresRemaining: attempt.refundFailures,
      };
      state.payments[payment.id] = payment;
      if (!authorized) queuePaymentEvent(state, payment);
      return payment;
    });
  }
}
