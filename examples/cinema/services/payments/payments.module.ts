import { type Keel } from "@marcelsud/keel.ts";
import { BookingServiceContract } from "../../contracts/generated/typescript/cinema/booking/v1/booking_keel.js";
import { ChargePaymentService } from "./charge-payment.service.js";
import { RefundPaymentService } from "./refund-payment.service.js";
import { PaymentsController } from "./payments.controller.js";
import { PaymentsRepository } from "./payments.repository.js";
import { SettlementService } from "./settlement.service.js";
import { OutboxPublisher } from "./outbox.publisher.js";
import { PaymentsWorker } from "./payments.worker.js";
export class PaymentsModule {
  constructor(private readonly app: Keel) {}
  register(): void {
    const repository = new PaymentsRepository(this.app.sql);
    const charges = new ChargePaymentService(
      repository,
      this.app.client(BookingServiceContract),
    );
    const refunds = new RefundPaymentService(repository);
    new PaymentsController(charges, refunds, repository).register(this.app);
    new PaymentsWorker(
      new SettlementService(repository),
      new OutboxPublisher(repository, this.app.pubsub),
    ).register(this.app);
  }
}
