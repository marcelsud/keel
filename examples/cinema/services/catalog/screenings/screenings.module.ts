import { type Keel, type ServiceClient } from "@marcelsud/keel.ts";
import { type BookingServiceContract } from "../../../contracts/generated/typescript/cinema/booking/v1/booking_keel.js";
import { CatalogRepository } from "../catalog.repository.js";
import { ScreeningsRepository } from "./screenings.repository.js";
import { ScreeningsService } from "./screenings.service.js";
import { ScreeningsController } from "./screenings.controller.js";
export class ScreeningsModule {
  readonly service: ScreeningsService;
  constructor(
    private readonly app: Keel,
    catalog: CatalogRepository,
    booking: ServiceClient<typeof BookingServiceContract.service>,
  ) {
    this.service = new ScreeningsService(
      new ScreeningsRepository(app.sql),
      catalog,
      booking,
    );
  }
  register(): void {
    new ScreeningsController(this.service).register(this.app);
  }
}
