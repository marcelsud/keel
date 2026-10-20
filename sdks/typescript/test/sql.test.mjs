import assert from "node:assert/strict";
import { test } from "node:test";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createKeel, SQLCommitUnknownError } from "../dist/index.js";

// Native SQL is opt-in; ordinary SDK publishing/tests need no C toolchain.
test("native SQL through NATS: TypeScript queries and transaction lifecycle", {
  skip: !process.env.KEEL_SQL_BINARY,
  timeout: 20_000,
}, async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "keel-ts-sql-"));
  const credentials = { admin: "test-admin-credential-0000000000", services: { alpha: "test-alpha-credential-0000000000" } };
  const file = join(directory, "credentials.json");
  await writeFile(file, JSON.stringify(credentials), { mode: 0o600 });
  const server = spawn(process.env.KEEL_SQL_BINARY, ["-sql", "-credentials", file, "-port", "-1", "-data", join(directory, "data")], { stdio: ["ignore", "ignore", "pipe"] });
  const exited = once(server, "exit");
  const apps = [];
  let errors = "";
  t.after(async () => {
    await Promise.all(apps.map((app) => app.stop()));
    if (server.exitCode === null && server.signalCode === null) server.kill("SIGTERM");
    const kill = setTimeout(() => server.kill("SIGKILL"), 4000);
    try { assert.deepEqual(await exited, [0, null], errors); }
    finally { clearTimeout(kill); await rm(directory, { recursive: true, force: true }); }
  });
  const url = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`core startup: ${errors}`)), 5000);
    server.stderr.on("data", (data) => {
      errors += data;
      const match = errors.match(/url=(nats:\/\/\S+)/);
      if (match) { clearTimeout(timer); resolve(match[1]); }
    });
    server.once("error", (error) => { clearTimeout(timer); reject(error); });
    server.once("exit", () => { clearTimeout(timer); reject(new Error(errors)); });
  });
  const admin = await createKeel({ name: "alpha", user: "keel-admin", token: credentials.admin, url });
  apps.push(admin);
  // Migrations belong to the CLI/admin identity; SQL queries use the same transport.
  const migration = {
    migrations: [{ name: "0001_test.sql", sql: "-- +goose Up\nCREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT NOT NULL);" }],
  };
  const { connect } = await import("@nats-io/transport-node");
  const nc = await connect({ servers: url, user: "keel-admin", pass: credentials.admin });
  try {
    const reply = await nc.request("keel.v1.sql.alpha.migrate", new TextEncoder().encode(JSON.stringify(migration)));
    assert.equal(reply.headers?.get("Nats-Service-Error-Code"), undefined);
  } finally { await nc.drain(); }
  const app = await createKeel({ name: "alpha", user: "alpha", token: credentials.services.alpha, url });
  apps.push(app);

  await t.test("parameters, result types, and callback return value", async () => {
    assert.equal((await app.sql.query("INSERT INTO notes VALUES (?,?)", [1, "2026-10-19"])).rowCount, 1);
    assert.deepEqual((await app.sql.query("SELECT body FROM notes WHERE id=?", [1])).rows, [{ body: "2026-10-19" }]);
    assert.equal(await app.sql.transaction(async (tx) => {
      await tx.query("INSERT INTO notes VALUES (?,?)", [2, "committed"]);
      return "done";
    }), "done");
    assert.deepEqual((await app.sql.query("SELECT 9223372036854775807 AS large, x'00ff' AS blob, ? AS flag", [true])).rows, [{ large: "9223372036854775807", blob: { base64: "AP8=" }, flag: 1 }]);
    assert.throws(() => app.sql.query("SELECT ?", [Number.MAX_SAFE_INTEGER + 1]), /large integers/);
    assert.throws(() => app.sql.query("SELECT ?", [Infinity]), /finite/);
  });
  await t.test("callback throws and caught query errors cannot commit", async () => {
    let escaped;
    await assert.rejects(app.sql.transaction(async (tx) => {
      escaped = tx;
      await tx.query("INSERT INTO notes VALUES (?,?)", [3, "rolled back"]);
      throw new Error("abort purchase");
    }), /abort purchase/);
    await assert.rejects(escaped.query("SELECT 1"), /closed/);
    await assert.rejects(app.sql.transaction(async (tx) => {
      await tx.query("INSERT INTO notes VALUES (?,?)", [4, "also rolled back"]);
      await assert.rejects(tx.query("INSERT INTO notes VALUES (?,?)", [1, "duplicate"]));
    }), /transaction failed/);
    assert.deepEqual((await app.sql.query("SELECT id FROM notes ORDER BY id")).rows, [{ id: 1 }, { id: 2 }]);
  });
  await t.test("bounded statements and reserved metadata", async () => {
    await assert.rejects(app.sql.query("DELETE FROM 'goose_db_version'"));
    await assert.rejects(app.sql.query("WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n) SELECT sum(x) FROM n"));
    assert.deepEqual((await app.sql.query("SELECT 1 AS healthy")).rows, [{ healthy: 1 }]);
  });
  await t.test("lost connection during commit reports unknown outcome", async () => {
    const broken = await createKeel({ name: "alpha", user: "alpha", token: credentials.services.alpha, url });
    apps.push(broken);
    await assert.rejects(broken.sql.transaction(async (tx) => {
      await tx.query("INSERT INTO notes VALUES (?,?)", [5, "uncertain"]);
      await broken.stop();
    }), SQLCommitUnknownError);
  });
});
