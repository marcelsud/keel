import { type Keel } from "@marcelsud/keel.ts";
import { BookingServiceContract } from "../../contracts/generated/typescript/cinema/booking/v1/booking_keel.js";
import { CatalogRepository } from "./catalog.repository.js";
import { CatalogService } from "./catalog.service.js";
import { CatalogController } from "./catalog.controller.js";
import { ScreeningsModule } from "./screenings/screenings.module.js";
import { seedFixtures } from "./fixtures.js";
export class CatalogModule {
  constructor(private readonly app: Keel) {}
  async register(): Promise<void> {
    const repository = new CatalogRepository(this.app.sql);
    const screenings = new ScreeningsModule(
      this.app,
      repository,
      this.app.client(BookingServiceContract),
    );
    new CatalogController(
      new CatalogService(repository, screenings.service),
    ).register(this.app);
    screenings.register();
    await seedFixtures(this.app);
  }
}
