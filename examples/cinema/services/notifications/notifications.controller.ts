import { type Keel } from "@marcelsud/keel.ts";
import { receiptQuery } from "./notifications.dto.js";
import { NotificationsService } from "./notifications.service.js";
export class NotificationsController {
  constructor(private readonly service: NotificationsService) {}
  register(app: Keel): void {
    app.action({
      name: "getReceipts",
      args: receiptQuery,
      handler: (_ctx, input) => this.service.receipts(input),
    });
  }
}
