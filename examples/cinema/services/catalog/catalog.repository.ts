import { type SQL } from "@marcelsud/keel.ts";
import { type Cinema, type Movie } from "../../packages/shared-ts/index.js";
type CinemaRow = Omit<Cinema, "auditoriums"> & { auditoriums: string };
const cinema = (row: CinemaRow): Cinema => ({
  ...row,
  auditoriums: JSON.parse(row.auditoriums),
});
export class CatalogRepository {
  constructor(private readonly sql: SQL) {}
  async listMovies(): Promise<Movie[]> {
    return (
      await this.sql.query<Movie>(
        "SELECT id, title, duration_ms AS durationMs FROM movies ORDER BY id",
      )
    ).rows;
  }
  async getMovie(id: string): Promise<Movie | undefined> {
    return (
      await this.sql.query<Movie>(
        "SELECT id, title, duration_ms AS durationMs FROM movies WHERE id = ?",
        [id],
      )
    ).rows[0];
  }
  async listCinemas(): Promise<Cinema[]> {
    return (
      await this.sql.query<CinemaRow>("SELECT * FROM cinemas ORDER BY id")
    ).rows.map(cinema);
  }
  async getCinema(id: string): Promise<Cinema | undefined> {
    const { rows } = await this.sql.query<CinemaRow>(
      "SELECT * FROM cinemas WHERE id = ?",
      [id],
    );
    return rows[0] ? cinema(rows[0]) : undefined;
  }
}
