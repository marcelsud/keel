import { CatalogRepository } from "./catalog.repository.js";
import { ScreeningsService } from "./screenings/screenings.service.js";
export class CatalogService {
  constructor(
    private readonly catalog: CatalogRepository,
    private readonly screenings: ScreeningsService,
  ) {}
  async list() {
    const [cinemas, movies, screenings] = await Promise.all([
      this.catalog.listCinemas(),
      this.catalog.listMovies(),
      this.screenings.list(),
    ]);
    return { cinemas, movies, screenings };
  }
}
