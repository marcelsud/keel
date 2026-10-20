import { PaymentsRepository } from "./payments.repository.js";
import { queuePaymentEvent } from "./payments.model.js";
export class SettlementService {
  constructor(private readonly repository: PaymentsRepository) {}
  async settle(): Promise<void> {
    await this.repository.update((state) => {
      for (const payment of Object.values(state.payments)) {
        if (payment.status === "PENDING" && payment.dueAtMs <= Date.now()) {
          payment.status = payment.mode === "decline" ? "DECLINED" : "APPROVED";
          queuePaymentEvent(state, payment);
        }
      }
    });
  }
}
