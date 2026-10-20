import { type Keel } from "@marcelsud/keel.ts";
import { periodic } from "../../packages/shared-ts/index.js";
import { SettlementService } from "./settlement.service.js";
import { OutboxPublisher } from "./outbox.publisher.js";
export class PaymentsWorker {
  constructor(
    private readonly settlement: SettlementService,
    private readonly outbox: OutboxPublisher,
  ) {}
  register(app: Keel): void {
    periodic(app, async () => {
      await this.settlement.settle();
      await this.outbox.dispatch();
    });
  }
}
