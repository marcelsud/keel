import { type Keel } from "@marcelsud/keel.ts";
import { ScreeningsService } from "./screenings.service.js";
import {
  screeningRef,
  scheduleScreening,
  setScreeningPrice,
} from "./screenings.dto.js";
export class ScreeningsController {
  constructor(private readonly service: ScreeningsService) {}
  register(app: Keel): void {
    app.action({
      name: "listScreenings",
      args: app.schema.object({}),
      handler: () => this.service.list(),
    });
    app.action({
      name: "getScreening",
      args: screeningRef,
      handler: (_ctx, input) => this.service.find(input.screeningId),
    });
    app.action({
      name: "schedule",
      args: scheduleScreening,
      handler: (_ctx, input) => this.service.schedule(input),
    });
    app.action({
      name: "setPrice",
      args: setScreeningPrice,
      handler: (_ctx, input) => this.service.setPrice(input),
    });
    app.action({
      name: "cancelScreening",
      args: screeningRef,
      handler: (_ctx, input) => this.service.cancel(input.screeningId),
    });
  }
}
