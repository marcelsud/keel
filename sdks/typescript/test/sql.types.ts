import type { Keel, SQLTransaction } from "../src/index.js";
declare const app: Keel;

app.sql.transaction(async (tx: SQLTransaction) => {
  const result = await tx.query<{ id: string }>(
    "SELECT id FROM notes WHERE id=?",
    ["note-1"],
  );
  const id: string | undefined = result.rows[0]?.id;
  // @ts-expect-error Transaction completion belongs to the callback lifecycle.
  tx.finish(true);
  // @ts-expect-error Parameters must be scalar values.
  await tx.query("SELECT ?", [{ id }]);
  return id;
});
