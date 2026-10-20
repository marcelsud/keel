import { type Keel } from "@marcelsud/keel.ts";
import { type BookingEvent } from "../../packages/shared-ts/index.js";
import { NotificationsService } from "./notifications.service.js";
export class NotificationsWorker {
  constructor(private readonly service: NotificationsService) {}
  register(app: Keel): void {
    app.worker<BookingEvent>("cinema.notify", (_ctx, event) =>
      this.service.deliver(event),
    );
  }
}
