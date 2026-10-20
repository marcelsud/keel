import { type Keel } from "@marcelsud/keel.ts";

const fixtures = [
  {
    collection: "cinema-movies",
    id: "orbit",
    document: { title: "Orbit", durationMs: 90 * 60_000 },
  },
  {
    collection: "cinema-movies",
    id: "midnight",
    document: { title: "Midnight Express", durationMs: 110 * 60_000 },
  },
  ...["downtown", "riverside"].map((id) => ({
    collection: "cinema-cinemas",
    id,
    document: {
      name: id === "downtown" ? "Downtown Cinema" : "Riverside Cinema",
      auditoriums: ["hall-1", "hall-2"].map((id) => ({
        id,
        seats: ["A", "B", "C", "D"].flatMap((row) =>
          Array.from({ length: 6 }, (_, index) => ({
            id: `${row}${index + 1}`,
            category: row === "A" ? "premium" : "standard",
            blocked: row === "D" && index === 5,
          })),
        ),
      })),
    },
  })),
];

export async function seedFixtures(app: Keel): Promise<void> {
  await app.sql.transaction(async (tx) => {
    if (
      (await tx.query("SELECT id FROM fixtures WHERE id = ?", ["catalog-v1"]))
        .rows.length
    )
      return;
    for (const fixture of fixtures) {
      if (fixture.collection === "cinema-movies") {
        const movie = fixture.document as { title: string; durationMs: number };
        await tx.query(
          "INSERT INTO movies (id,title,duration_ms) VALUES (?,?,?) ON CONFLICT(id) DO NOTHING",
          [fixture.id, movie.title, movie.durationMs],
        );
      } else {
        const cinema = fixture.document as {
          name: string;
          auditoriums: unknown;
        };
        await tx.query(
          "INSERT INTO cinemas (id,name,auditoriums) VALUES (?,?,?) ON CONFLICT(id) DO NOTHING",
          [fixture.id, cinema.name, JSON.stringify(cinema.auditoriums)],
        );
      }
    }
    await tx.query("INSERT INTO fixtures VALUES (?)", ["catalog-v1"]);
  });
}
