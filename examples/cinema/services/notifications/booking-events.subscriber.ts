import { type Keel } from "@marcelsud/keel.ts";
import { type BookingEvent } from "../../packages/shared-ts/index.js";
import { validateBookingEvent } from "./notifications.dto.js";
export class BookingEventsSubscriber {
  register(app: Keel): void {
    app.subscription<BookingEvent>(
      "cinema.booking",
      async (ctx, event) => {
        validateBookingEvent(event);
        await ctx.queue.enqueue("cinema.notify", event);
      },
      { name: "cinema-notification-dispatch" },
    );
  }
}
