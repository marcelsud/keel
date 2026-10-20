import { type Keel } from "@marcelsud/keel.ts";
import { NotificationsRepository } from "./notifications.repository.js";
import { NotificationsService } from "./notifications.service.js";
import { NotificationsController } from "./notifications.controller.js";
import { NotificationsWorker } from "./notifications.worker.js";
import { BookingEventsSubscriber } from "./booking-events.subscriber.js";
export class NotificationsModule {
  constructor(private readonly app: Keel) {}
  register(): void {
    const failures = Number(process.env.CINEMA_NOTIFICATION_FAILURES ?? "0");
    if (!Number.isSafeInteger(failures) || failures < 0 || failures > 5)
      throw new Error("CINEMA_NOTIFICATION_FAILURES must be 0–5");
    const service = new NotificationsService(
      new NotificationsRepository(this.app.sql),
      failures,
    );
    new NotificationsController(service).register(this.app);
    new BookingEventsSubscriber().register(this.app);
    new NotificationsWorker(service).register(this.app);
  }
}
