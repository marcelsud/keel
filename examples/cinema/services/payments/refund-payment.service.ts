import { fail, own } from "../../packages/shared-ts/index.js";
import { queuePaymentEvent, type Payment } from "./payments.model.js";
import { PaymentsRepository } from "./payments.repository.js";
export class RefundPaymentService {
  constructor(private readonly repository: PaymentsRepository) {}
  async refund(paymentId: string): Promise<Payment> {
    const result = await this.repository.update((state) => {
      const payment =
        own(state.payments, paymentId) ?? fail(404, "payment not found");
      if (payment.status === "REFUNDED") return { payment, failed: false };
      if (payment.status !== "APPROVED")
        fail(409, "only approved payments can be refunded");
      if (payment.refundFailuresRemaining > 0) {
        payment.refundFailuresRemaining--;
        return { payment, failed: true };
      }
      payment.status = "REFUNDED";
      payment.refundCount++;
      queuePaymentEvent(state, payment);
      return { payment, failed: false };
    });
    if (result.failed)
      fail(503, "simulated refund failure; retry the same paymentId");
    return result.payment;
  }
}
