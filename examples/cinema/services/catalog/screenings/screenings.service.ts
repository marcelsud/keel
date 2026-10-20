import { isDeepStrictEqual } from "node:util";
import { type ServiceClient } from "@marcelsud/keel.ts";
import { type BookingServiceContract } from "../../../contracts/generated/typescript/cinema/booking/v1/booking_keel.js";
import {
  fail,
  identifier,
  own,
  type Screening,
} from "../../../packages/shared-ts/index.js";
import { CatalogRepository } from "../catalog.repository.js";
import { ScreeningsRepository } from "./screenings.repository.js";
import {
  type ScheduleScreeningDto,
  type SetScreeningPriceDto,
} from "./screenings.dto.js";
export class ScreeningsService {
  constructor(
    private readonly repository: ScreeningsRepository,
    private readonly catalog: CatalogRepository,
    private readonly booking: ServiceClient<
      typeof BookingServiceContract.service
    >,
  ) {}
  async list(): Promise<Screening[]> {
    return Object.values((await this.repository.read()).screenings);
  }
  async find(id: string): Promise<Screening> {
    return (
      own((await this.repository.read()).screenings, id) ??
      fail(404, "screening not found")
    );
  }
  async schedule(input: ScheduleScreeningDto): Promise<Screening> {
    identifier(input.id, "id");
    const [cinema, movie] = await Promise.all([
      this.catalog.getCinema(input.cinemaId),
      this.catalog.getMovie(input.movieId),
    ]);
    const auditorium = cinema?.auditoriums.find(
      (room) => room.id === input.auditoriumId,
    );
    if (!auditorium || !movie)
      fail(400, "unknown cinema, auditorium, or movie");
    const holdMs = input.holdMs ?? 5 * 60_000,
      cancelCutoffMs = input.cancelCutoffMs ?? 60 * 60_000;
    if (
      input.startsAtMs <= Date.now() ||
      input.startsAtMs > Date.now() + 365 * 24 * 60 * 60_000 ||
      input.basePriceMinor < 1 ||
      input.basePriceMinor > 100_000 ||
      !/^[A-Z]{3}$/.test(input.currency) ||
      holdMs < 100 ||
      holdMs > 30 * 60_000 ||
      cancelCutoffMs < 0 ||
      cancelCutoffMs > 7 * 24 * 60 * 60_000
    )
      fail(
        400,
        "invalid schedule, price, currency, hold, or cancellation cutoff",
      );
    const screening: Screening = {
      ...input,
      holdMs,
      cancelCutoffMs,
      endsAtMs: input.startsAtMs + movie.durationMs + 15 * 60_000,
      canceled: false,
      seats: auditorium.seats.map((seat) => ({
        ...seat,
        priceMinor:
          input.basePriceMinor + (seat.category === "premium" ? 500 : 0),
      })),
    };
    return this.repository.update((state) => {
      const existing = own(state.screenings, input.id);
      if (existing) {
        if (!isDeepStrictEqual(existing, screening))
          fail(409, "screening id already exists with different settings");
        return existing;
      }
      if (Object.keys(state.screenings).length >= 100)
        fail(409, "prototype limit: 100 screenings");
      if (
        Object.values(state.screenings).some(
          (other) =>
            !other.canceled &&
            other.cinemaId === input.cinemaId &&
            other.auditoriumId === input.auditoriumId &&
            other.startsAtMs < screening.endsAtMs &&
            screening.startsAtMs < other.endsAtMs,
        )
      )
        fail(409, "screening overlaps another screening or its cleanup buffer");
      state.screenings[input.id] = screening;
      return screening;
    });
  }
  async setPrice(input: SetScreeningPriceDto): Promise<Screening> {
    if (input.basePriceMinor < 1 || input.basePriceMinor > 100_000)
      fail(400, "invalid price");
    return this.repository.update((state) => {
      const screening =
        own(state.screenings, input.screeningId) ??
        fail(404, "screening not found");
      screening.basePriceMinor = input.basePriceMinor;
      for (const seat of screening.seats)
        seat.priceMinor =
          input.basePriceMinor + (seat.category === "premium" ? 500 : 0);
      return screening;
    });
  }
  async cancel(screeningId: string): Promise<Screening> {
    await this.find(screeningId);
    await this.booking.cancelScreening({ screeningId });
    return this.repository.update((state) => {
      state.screenings[screeningId]!.canceled = true;
      return state.screenings[screeningId]!;
    });
  }
}
