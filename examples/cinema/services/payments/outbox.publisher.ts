import { type Keel } from "@marcelsud/keel.ts";
import { PaymentsRepository } from "./payments.repository.js";
export class OutboxPublisher {
  constructor(
    private readonly repository: PaymentsRepository,
    private readonly pubsub: Keel["pubsub"],
  ) {}
  async dispatch(): Promise<void> {
    for (const item of Object.values((await this.repository.read()).outbox)) {
      for (let i = 0; i < item.copies; i++)
        await this.pubsub.publish("cinema.payment", item);
      await this.repository.update((state) => {
        delete state.outbox[item.id];
      });
    }
  }
}
