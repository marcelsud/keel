import { type SQL, type SQLTransaction } from "@marcelsud/keel.ts";
import { type Screening } from "../../../packages/shared-ts/index.js";
export type Schedule = { screenings: Record<string, Screening> };
type ScreeningRow = Omit<Screening, "seats" | "canceled"> & {
  seats: string;
  canceled: number;
};

export class ScreeningsRepository {
  constructor(private readonly sql: SQL) {}
  read(): Promise<Schedule> {
    return this.readFrom(this.sql);
  }

  private async readFrom(connection: SQL | SQLTransaction): Promise<Schedule> {
    const { rows } =
      await connection.query<ScreeningRow>(`SELECT id,cinema_id AS cinemaId,auditorium_id AS auditoriumId,movie_id AS movieId,
      starts_at_ms AS startsAtMs,ends_at_ms AS endsAtMs,base_price_minor AS basePriceMinor,currency,
      hold_ms AS holdMs,cancel_cutoff_ms AS cancelCutoffMs,canceled,seats FROM screenings ORDER BY id`);
    return {
      screenings: Object.fromEntries(
        rows.map((row) => [
          row.id,
          {
            ...row,
            canceled: Boolean(row.canceled),
            seats: JSON.parse(row.seats),
          },
        ]),
      ),
    };
  }

  update<T>(change: (state: Schedule) => T): Promise<T> {
    return this.sql.transaction(async (tx) => {
      const state = await this.readFrom(tx);
      const before = new Map(
        Object.entries(state.screenings).map(([id, item]) => [
          id,
          JSON.stringify(item),
        ]),
      );
      const result = change(state);
      for (const screening of Object.values(state.screenings)) {
        if (before.get(screening.id) === JSON.stringify(screening)) continue;
        await tx.query(
          `INSERT INTO screenings(id,cinema_id,auditorium_id,movie_id,starts_at_ms,ends_at_ms,base_price_minor,currency,hold_ms,cancel_cutoff_ms,canceled,seats)
          VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET base_price_minor=excluded.base_price_minor,canceled=excluded.canceled,seats=excluded.seats`,
          [
            screening.id,
            screening.cinemaId,
            screening.auditoriumId,
            screening.movieId,
            screening.startsAtMs,
            screening.endsAtMs,
            screening.basePriceMinor,
            screening.currency,
            screening.holdMs,
            screening.cancelCutoffMs,
            screening.canceled,
            JSON.stringify(screening.seats),
          ],
        );
      }
      return result;
    });
  }
}
