import { type Keel } from "@marcelsud/keel.ts";
import { PaymentsServiceContract } from "../../contracts/generated/typescript/cinema/payments/v1/payments_keel.js";
import { ChargePaymentService } from "./charge-payment.service.js";
import { RefundPaymentService } from "./refund-payment.service.js";
import { PaymentsRepository } from "./payments.repository.js";
export class PaymentsController {
  constructor(
    private readonly charges: ChargePaymentService,
    private readonly refunds: RefundPaymentService,
    private readonly repository: PaymentsRepository,
  ) {}
  register(app: Keel): void {
    app.service(PaymentsServiceContract, {
      charge: (_ctx, input) => this.charges.charge(input),
      refund: (_ctx, input) => this.refunds.refund(input.paymentId),
      get: (_ctx, input) => this.repository.get(input.paymentId),
    });
  }
}
