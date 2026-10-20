import { afterAll, beforeAll, expect, test } from "bun:test";
import assert from "node:assert/strict";
import { spawn, execFileSync, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createServer } from "node:net";
import { createKeel, type Keel } from "@marcelsud/keel.ts";
import { BookingServiceContract } from "../contracts/generated/typescript/cinema/booking/v1/booking_keel.js";
import { PaymentsServiceContract } from "../contracts/generated/typescript/cinema/payments/v1/payments_keel.js";
import {
  directory,
  startServices,
  stopServices,
  type RunningService,
  type Credentials,
} from "../tools/dev.js";
import {
  type Cinema,
  type Movie,
  type Screening,
} from "../packages/shared-ts/index.js";
import { seedFixtures } from "../services/catalog/fixtures.js";

type Order = {
  id: string;
  screeningId: string;
  customerId: string;
  requestId: string;
  status: string;
  totalMinor: number;
  currency: string;
  expiresAtMs: number;
  paymentId: string;
  refundStatus: string;
  seats: { id: string; priceMinor: number }[];
  tickets: { id: string; seatId: string; valid: boolean }[];
  attempts: { id: string; status: string }[];
};
let data: string,
  coreNodes: ChildProcess[],
  coreExited: Promise<unknown>,
  app: Keel,
  services: RunningService[],
  environment: NodeJS.ProcessEnv;
let counter = 0;
let catalogDB: Keel, credentials: Credentials;
const baseTime = Date.now() + 2 * 60 * 60_000;
const call = <T = Order>(action: string, input: unknown) =>
  app.call<T>("cinema-checkout", action, input);
const ref = (order: Order) => ({
  screeningId: order.screeningId,
  orderId: order.id,
  customerId: order.customerId,
});
const status = (code: number) => (error: unknown) =>
  typeof error === "object" &&
  error !== null &&
  "code" in error &&
  error.code === code;

async function eventually<T>(
  read: () => Promise<T>,
  ready: (value: T) => boolean,
  timeout = 10_000,
): Promise<T> {
  const deadline = Date.now() + timeout;
  let value: T | undefined;
  while (Date.now() < deadline) {
    try {
      value = await read();
      if (ready(value)) return value;
    } catch (error) {
      if (!status(409)(error) && !status(503)(error)) throw error;
    }
    await Bun.sleep(60);
  }
  throw new Error(
    `condition did not settle: ${JSON.stringify(value)}\n${services.map((service) => service.output.slice(-1500)).join("\n")}`,
  );
}

async function screening(
  overrides: Record<string, unknown> = {},
): Promise<Screening> {
  const id = `screening-${++counter}`;
  return app.call<Screening>("cinema-catalog", "schedule", {
    id,
    cinemaId: "downtown",
    auditoriumId: "hall-1",
    movieId: "orbit",
    startsAtMs: baseTime + counter * 4 * 60 * 60_000,
    basePriceMinor: 1200,
    currency: "USD",
    holdMs: 10_000,
    ...overrides,
  });
}
async function reserve(
  show: Screening,
  customerId = "ada",
  seatIds = ["B1"],
  requestId = `hold-${customerId}`,
): Promise<Order> {
  return call("reserve", {
    screeningId: show.id,
    customerId,
    seatIds,
    requestId,
  });
}
async function pay(
  order: Order,
  overrides: Record<string, unknown> = {},
): Promise<Order> {
  await call("pay", {
    ...ref(order),
    requestId: "pay-1",
    mode: "approve",
    ...overrides,
  });
  return eventually(
    () => call("getOrder", ref(order)),
    (next) => next.status === "CONFIRMED",
  );
}

async function startCore(data: string, environment: NodeJS.ProcessEnv) {
  const names = [
    "catalog",
    "booking",
    "payments",
    "notifications",
    "checkout",
    "client",
  ];
  const credentials: Credentials = {
    admin: randomBytes(32).toString("hex"),
    services: Object.fromEntries(
      names.map((name) => [`cinema-${name}`, randomBytes(32).toString("hex")]),
    ),
  };
  const credentialFile = join(data, "credentials.json");
  await writeFile(credentialFile, JSON.stringify(credentials), { mode: 0o600 });
  const nodes = environment.KEEL_SQL_ENGINE === "dqlite" ? 3 : 1;
  const freeAddress = async () => {
    const listener = createServer();
    listener.listen(0, "127.0.0.1");
    await once(listener, "listening");
    const address = listener.address();
    if (!address || typeof address === "string")
      throw new Error("missing cluster address");
    const value = `127.0.0.1:${address.port}`;
    await new Promise<void>((resolve, reject) =>
      listener.close((error) => (error ? reject(error) : resolve())),
    );
    return value;
  };
  const routes = await Promise.all(Array.from({ length: nodes }, freeAddress));
  const sql = await Promise.all(Array.from({ length: nodes }, freeAddress));
  const children: ChildProcess[] = [];
  const start = async (index: number) => {
    let clusterArgs: string[] = [];
    if (nodes > 1) {
      const file = join(data, `node-${index}.json`);
      await writeFile(
        file,
        JSON.stringify({
          name: "cinema-test",
          node: `node-${index}`,
          listen: routes[index],
          routes: routes
            .filter((_, i) => i !== index)
            .map((address) => `nats-route://${address}`),
          sqlListen: sql[index],
          sqlPeers: index === 0 ? [] : [sql[0]],
          replicas: 3,
        }),
      );
      clusterArgs = ["-cluster-config", file];
    }
    const core = spawn(
      join(directory, ".keel/bin/keel"),
      [
        "-data",
        nodes === 1 ? data : join(data, `node-${index}`),
        "-port",
        "-1",
        "-sql",
        "-sql-transaction-timeout",
        "2s",
        "-credentials",
        credentialFile,
        ...clusterArgs,
      ],
      { env: environment, stdio: ["ignore", "ignore", "pipe"] },
    );
    children.push(core);
    const exited = once(core, "exit");
    const url = await new Promise<string>((resolve, reject) => {
      let output = "";
      const timer = setTimeout(
        () => reject(new Error(output)),
        nodes === 1 ? 10_000 : 25_000,
      );
      core.stderr!.on("data", (chunk) => {
        output += chunk;
        const match = output.match(/url=(nats:\/\/\S+)/);
        if (match) {
          clearTimeout(timer);
          resolve(match[1]!);
        }
      });
      core.once("error", (error) => {
        clearTimeout(timer);
        reject(error);
      });
      core.once("exit", () => {
        clearTimeout(timer);
        reject(new Error(output));
      });
    });
    return { core, exited, url };
  };
  let started: Awaited<ReturnType<typeof start>>[];
  try {
    started = await Promise.all(
      Array.from({ length: nodes }, (_, i) => start(i)),
    );
  } catch (error) {
    for (const child of children) child.kill("SIGKILL");
    throw error;
  }
  const url = started.map((node) => node.url).join(",");
  execFileSync(
    join(directory, ".keel/bin/keel"),
    ["migrate", "up", "--credentials", credentialFile],
    { cwd: directory, env: { ...environment, KEEL_URL: url }, stdio: "pipe" },
  );
  return {
    nodes: started.map((node) => node.core),
    exited: Promise.all(started.map((node) => node.exited)),
    url,
    credentials,
    credentialFile,
  };
}

beforeAll(async () => {
  data = await mkdtemp(join(tmpdir(), "keel-cinema-test-"));
  environment = {
    ...process.env,
    KEEL_TOKEN: "cinema-test",
    CINEMA_NOTIFICATION_FAILURES: "5",
  };
  const started = await startCore(data, environment);
  coreNodes = started.nodes;
  coreExited = started.exited;
  environment.KEEL_URL = started.url;
  environment.KEEL_CREDENTIALS_FILE = started.credentialFile;
  credentials = started.credentials;
  services = await startServices(environment, false);
  app = await createKeel({
    name: "cinema-tests",
    url: environment.KEEL_URL,
    user: "cinema-client",
    token: credentials.services["cinema-client"],
  });
  catalogDB = await createKeel({
    name: "cinema-catalog",
    user: "cinema-catalog",
    url: started.url,
    token: credentials.services["cinema-catalog"],
  });
}, 30_000);

afterAll(async () => {
  await app?.stop();
  await catalogDB?.stop();
  if (services) await stopServices(services);
  for (const core of coreNodes ?? [])
    if (core.exitCode === null && core.signalCode === null)
      core.kill("SIGTERM");
  if (coreExited) await coreExited;
  if (data) await rm(data, { recursive: true, force: true });
}, 15_000);

test("Catalog is TypeScript/JSON, validates schedules, and prevents auditorium overlaps", async () => {
  const catalog = await call<{
    cinemas: { auditoriums: unknown[] }[];
    movies: unknown[];
  }>("list", {});
  expect(catalog.cinemas).toHaveLength(2);
  expect(
    catalog.cinemas.every((cinema) => cinema.auditoriums.length === 2),
  ).toBe(true);
  expect(catalog.movies).toHaveLength(2);
  expect(
    (
      await catalogDB.sql.query("SELECT id FROM fixtures WHERE id = ?", [
        "catalog-v1",
      ])
    ).rows,
  ).toHaveLength(1);
  await assert.rejects(
    app.call("cinema-catalog", "schedule", { id: 42 }),
    status(400),
  );
  await assert.rejects(
    app.call("cinema-catalog", "setPrice", {
      screeningId: "__proto__",
      basePriceMinor: 1200,
    }),
    status(404),
  );
  const first = await screening();
  const repeated = await app.call<Screening>("cinema-catalog", "schedule", {
    id: first.id,
    cinemaId: first.cinemaId,
    auditoriumId: first.auditoriumId,
    movieId: first.movieId,
    startsAtMs: first.startsAtMs,
    basePriceMinor: first.basePriceMinor,
    currency: first.currency,
    holdMs: first.holdMs,
    cancelCutoffMs: first.cancelCutoffMs,
  });
  expect(repeated).toEqual(first);
  const competing = await Promise.allSettled(
    ["overlap-a", "overlap-b"].map((id) =>
      app.call("cinema-catalog", "schedule", {
        id,
        cinemaId: "riverside",
        auditoriumId: "hall-1",
        movieId: "orbit",
        startsAtMs: first.startsAtMs,
        basePriceMinor: 1200,
        currency: "USD",
      }),
    ),
  );
  expect(
    competing.filter((result) => result.status === "fulfilled"),
  ).toHaveLength(1);
  expect(
    competing.find((result) => result.status === "rejected")!.reason.code,
  ).toBe(409);
  await assert.rejects(
    screening({ startsAtMs: first.endsAtMs - 1 }),
    status(409),
  );
});

test("Catalog lists and schedules movies and cinemas stored in SQL", async () => {
  const movieId = "sql-movie",
    cinemaId = "sql-cinema";
  await catalogDB.sql.query("INSERT INTO movies VALUES (?,?,?)", [
    movieId,
    "SQL premiere",
    60_000,
  ]);
  const existing = (
    await catalogDB.sql.query<{ auditoriums: string }>(
      "SELECT auditoriums FROM cinemas WHERE id = ?",
      ["downtown"],
    )
  ).rows[0]!;
  await catalogDB.sql.query("INSERT INTO cinemas VALUES (?,?,?)", [
    cinemaId,
    "SQL cinema",
    existing.auditoriums,
  ]);
  const catalog = await call<{ cinemas: Cinema[]; movies: Movie[] }>(
    "list",
    {},
  );
  expect(catalog.movies.find((movie) => movie.id === movieId)?.title).toBe(
    "SQL premiere",
  );
  expect(catalog.cinemas.find((cinema) => cinema.id === cinemaId)?.name).toBe(
    "SQL cinema",
  );
  const show = await screening({ movieId, cinemaId });
  expect(show.endsAtMs - show.startsAtMs).toBe(16 * 60_000);
  expect(show.seats).toHaveLength(24);
});

test("transactional fixtures seed concurrently and preserve existing edits and deletions", async () => {
  const data = await mkdtemp(join(tmpdir(), "keel-cinema-fixtures-"));
  const started = await startCore(data, environment);
  let fast: Keel | undefined, slow: Keel | undefined;
  try {
    const options = {
      name: "cinema-catalog",
      user: "cinema-catalog",
      url: started.url,
      token: started.credentials.services["cinema-catalog"],
    };
    fast = await createKeel(options);
    slow = await createKeel(options);
    await fast.sql.query("INSERT INTO movies VALUES (?,?,?)", [
      "orbit",
      "Imported Orbit",
      60_000,
    ]);
    await Promise.all([seedFixtures(fast), seedFixtures(slow)]);
    expect((await fast.sql.query("SELECT id FROM fixtures")).rows).toHaveLength(
      1,
    );
    expect(
      (await fast.sql.query("SELECT title FROM movies WHERE id = ?", ["orbit"]))
        .rows[0]?.title,
    ).toBe("Imported Orbit");
    await fast.sql.query("UPDATE movies SET title = ? WHERE id = ?", [
      "Edited Orbit",
      "orbit",
    ]);
    await fast.sql.query("DELETE FROM movies WHERE id = ?", ["midnight"]);
    await Promise.all([seedFixtures(fast), seedFixtures(slow)]);
    expect(
      (await fast.sql.query("SELECT title FROM movies WHERE id = ?", ["orbit"]))
        .rows[0]?.title,
    ).toBe("Edited Orbit");
    expect(
      (await fast.sql.query("SELECT id FROM movies WHERE id = ?", ["midnight"]))
        .rows,
    ).toHaveLength(0);
    const cinema = (
      await fast.sql.query<{ auditoriums: string }>(
        "SELECT auditoriums FROM cinemas WHERE id = ?",
        ["riverside"],
      )
    ).rows[0]!;
    expect(
      JSON.parse(cinema.auditoriums).flatMap(
        (room: { seats: unknown[] }) => room.seats,
      ),
    ).toHaveLength(48);
  } finally {
    await slow?.stop();
    await fast?.stop();
    for (const node of started.nodes) node.kill("SIGTERM");
    await started.exited;
    await rm(data, { recursive: true, force: true });
  }
}, 30_000);

test("two Booking replicas prevent double booking and keep seat selections atomic", async () => {
  const show = await screening();
  const child = spawn(join(directory, ".keel/bin/booking"), [], {
    env: {
      ...environment,
      KEEL_USER: "cinema-booking",
      KEEL_TOKEN: credentials.services["cinema-booking"],
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  const exited = once(child, "exit");
  try {
    const [output] = await once(child.stdout!, "data");
    assert.match(String(output), /cinema-booking ready/);
    const outcomes = await Promise.allSettled(
      Array.from({ length: 24 }, (_, i) =>
        reserve(show, `customer-${i}`, ["B1"]),
      ),
    );
    const winners = outcomes.filter(
      (result): result is PromiseFulfilledResult<Order> =>
        result.status === "fulfilled",
    );
    expect(winners).toHaveLength(1);
    for (const result of outcomes)
      if (result.status === "rejected") expect(result.reason.code).toBe(409);
    const winner = winners[0]!.value;
    const replay = await reserve(show, winner.customerId, ["B1"]);
    expect(replay.id).toBe(winner.id);
    expect(replay.expiresAtMs).toBe(winner.expiresAtMs);
    await assert.rejects(reserve(show, winner.customerId, ["B2"]), status(409));
    await assert.rejects(reserve(show, "partial", ["B1", "B2"]), status(409));
    expect(
      (await reserve(show, "other", ["B2"])).seats.map((seat) => seat.id),
    ).toEqual(["B2"]);
    await assert.rejects(reserve(show, "blocked", ["D6"]), status(409));
    await assert.rejects(reserve(show, "duplicate", ["B3", "B3"]), status(400));
    await assert.rejects(
      call("getOrder", { ...ref(winner), customerId: "another-customer" }),
      status(404),
    );
  } finally {
    child.kill("SIGTERM");
    await exited;
  }
});

test("idempotency identities cannot alias across customers with delimiter-containing IDs", async () => {
  const show = await screening({ holdMs: 30_000 });
  const first = await reserve(show, "a", ["B1"], "b\u0000c");
  await assert.rejects(reserve(show, "a\u0000b", ["B1"], "c"), status(409));
  const second = await reserve(show, "a\u0000b", ["B2"], "c");
  expect(second.id).not.toBe(first.id);
  expect(second.customerId).toBe("a\u0000b");
  await assert.rejects(
    call("getOrder", { ...ref(first), customerId: second.customerId }),
    status(404),
  );
  const paid = await pay(second);
  expect(paid.customerId).toBe(second.customerId);
  const receipts = await eventually(
    () =>
      call<{ event: { customerId: string } }[]>("receipts", {
        orderId: second.id,
        customerId: second.customerId,
      }),
    (items) => items.length === 1,
  );
  expect(receipts[0]?.event.customerId).toBe(second.customerId);
}, 20_000);

test("quotes are fixed while availability and future holds use the new price", async () => {
  const show = await screening();
  const old = await reserve(show, "old-price", ["A1", "B1"]);
  expect(old.totalMinor).toBe(2900);
  await app.call("cinema-catalog", "setPrice", {
    screeningId: show.id,
    basePriceMinor: 2000,
  });
  const availability = await call<{
    seats: { id: string; priceMinor: number }[];
  }>("availability", { screeningId: show.id });
  expect(availability.seats.find((seat) => seat.id === "B2")!.priceMinor).toBe(
    2000,
  );
  const next = await reserve(show, "new-price", ["A2", "B2"]);
  expect(next.totalMinor).toBe(4500);
  expect((await call("getOrder", ref(old))).totalMinor).toBe(2900);
});

test("JSON Checkout calls protobuf services; duplicate payment requests and callbacks issue one ticket per seat", async () => {
  const show = await screening();
  const order = await reserve(show, "happy", ["A1", "A2"]);
  const request = {
    ...ref(order),
    requestId: "purchase",
    mode: "duplicate_callback",
    delayMs: 300,
  };
  await Promise.all(Array.from({ length: 6 }, () => call("pay", request)));
  const confirmed = await eventually(
    () => call("getOrder", ref(order)),
    (next) => next.status === "CONFIRMED",
  );
  expect(confirmed.tickets).toHaveLength(2);
  expect(new Set(confirmed.tickets.map((ticket) => ticket.id)).size).toBe(2);
  expect(confirmed.tickets.every((ticket) => ticket.valid)).toBe(true);
  const payment = await app
    .client(PaymentsServiceContract)
    .get({ paymentId: confirmed.paymentId });
  expect(payment.chargeCount).toBe(1);
  expect(payment.amountMinor).toBe(3400);
  const receipts = await eventually(
    () =>
      call<{ event: { kind: string }; attempts: number }[]>("receipts", {
        orderId: order.id,
        customerId: order.customerId,
      }),
    (list) => list.some((item) => item.event.kind === "confirmed"),
  );
  expect(
    receipts.filter((item) => item.event.kind === "confirmed"),
  ).toHaveLength(1);
  expect(receipts[0]!.attempts).toBe(6);
  await app.pubsub.publish("cinema.payment", {
    id: "duplicate",
    screeningId: show.id,
    orderId: order.id,
    customerId: order.customerId,
  });
  await Bun.sleep(200);
  expect((await call("getOrder", ref(order))).tickets).toHaveLength(2);
}, 15_000);

test("declined payments allow a new attempt, while uncertain payments prevent another charge", async () => {
  const show = await screening();
  const order = await reserve(show, "declined");
  await call("pay", {
    ...ref(order),
    requestId: "declined-1",
    mode: "decline",
    delayMs: 250,
  });
  await assert.rejects(
    call("pay", { ...ref(order), requestId: "another", mode: "approve" }),
    status(409),
  );
  const declined = await eventually(
    () => call("getOrder", ref(order)),
    (next) => next.status === "HELD" && next.attempts[0]?.status === "DECLINED",
  );
  const confirmed = await pay(declined, { requestId: "approved-2" });
  expect(confirmed.attempts).toHaveLength(2);
  expect(confirmed.tickets).toHaveLength(1);
});

test("holds expire without revival and release all seats", async () => {
  const show = await screening({ holdMs: 200 });
  const order = await reserve(show, "expires", ["B1", "B2"]);
  await eventually(
    () => call("getOrder", ref(order)),
    (next) => next.status === "EXPIRED",
  );
  expect((await reserve(show, "expires", ["B1", "B2"])).status).toBe("EXPIRED");
  expect((await reserve(show, "replacement", ["B1", "B2"])).status).toBe(
    "HELD",
  );
});

test("late payment approval refunds the old order and cannot steal a reallocated seat", async () => {
  const show = await screening({ holdMs: 500 });
  const old = await reserve(show, "late");
  await call("pay", {
    ...ref(old),
    requestId: "late-payment",
    mode: "approve",
    delayMs: 1200,
  });
  await eventually(
    () => call("getOrder", ref(old)),
    (next) => next.status === "EXPIRED",
  );
  const replacement = await reserve(show, "replacement");
  const sold = await pay(replacement, { delayMs: 0 });
  expect(sold.status).toBe("CONFIRMED");
  const refunded = await eventually(
    () => call("getOrder", ref(old)),
    (next) => next.refundStatus === "REFUNDED",
  );
  expect(refunded.tickets).toHaveLength(0);
  expect(
    (await call("getOrder", ref(replacement))).tickets.every(
      (ticket) => ticket.valid,
    ),
  ).toBe(true);
  const payment = await app
    .client(PaymentsServiceContract)
    .get({ paymentId: refunded.paymentId });
  expect(payment.chargeCount).toBe(1);
  expect(payment.refundCount).toBe(1);
});

test("a lost payment callback is repaired by reconciliation", async () => {
  const show = await screening();
  const order = await reserve(show, "lost-callback");
  const confirmed = await pay(order, { mode: "lost_callback", delayMs: 100 });
  expect(confirmed.status).toBe("CONFIRMED");
  expect(confirmed.tickets).toHaveLength(1);
});

test("full cancellation releases seats immediately and retries refunds without duplication", async () => {
  const show = await screening();
  const order = await pay(await reserve(show, "cancel", ["B1", "B2"]), {
    refundFailures: 2,
  });
  const canceled = await call("cancel", ref(order));
  expect(canceled.status).toBe("CANCELED");
  expect(canceled.tickets.every((ticket) => !ticket.valid)).toBe(true);
  expect(canceled.refundStatus).toBe("PENDING");
  expect((await reserve(show, "replacement", ["B1", "B2"])).status).toBe(
    "HELD",
  );
  await eventually(
    () => call("getOrder", ref(order)),
    (next) => next.refundStatus === "REFUNDED",
  );
  await call("cancel", ref(order));
  const refunded = await app
    .client(PaymentsServiceContract)
    .refund({ paymentId: order.paymentId });
  expect(refunded.refundCount).toBe(1);
  expect(refunded.refundFailuresRemaining).toBe(0);
});

test("customer cutoff is enforced; operator screening cancellation refunds orders and stops sales", async () => {
  const show = await screening({ cancelCutoffMs: 7 * 24 * 60 * 60_000 });
  const confirmed = await pay(await reserve(show, "cutoff", ["B1"]));
  const held = await reserve(show, "operator-held", ["B2"]);
  await assert.rejects(call("cancel", ref(confirmed)), status(409));
  await app.call("cinema-catalog", "cancelScreening", { screeningId: show.id });
  await app.call("cinema-catalog", "cancelScreening", { screeningId: show.id });
  await eventually(
    () => call("getOrder", ref(confirmed)),
    (next) => next.status === "CANCELED" && next.refundStatus === "REFUNDED",
  );
  expect((await call("getOrder", ref(held))).status).toBe("CANCELED");
  await assert.rejects(reserve(show, "too-late", ["B3"]), status(409));
  const availability = await app
    .client(BookingServiceContract)
    .getAvailability({ screeningId: show.id });
  expect(availability.canceled).toBe(true);
  expect(availability.seats.every((seat) => seat.status === "BLOCKED")).toBe(
    true,
  );
});

test("cancellation racing a payment cannot leave valid tickets or reclaim released seats", async () => {
  const show = await screening();
  const order = await reserve(show, "race");
  await call("pay", {
    ...ref(order),
    requestId: "race-pay",
    mode: "duplicate_callback",
    delayMs: 100,
  });
  await call("cancel", ref(order));
  const next = await reserve(show, "race-replacement");
  await pay(next);
  const canceled = await eventually(
    () => call("getOrder", ref(order)),
    (next) =>
      next.status === "CANCELED" &&
      ["REFUNDED", "NONE"].includes(next.refundStatus) &&
      next.attempts.every((attempt) =>
        ["DECLINED", "REFUNDED"].includes(attempt.status),
      ),
  );
  expect(canceled.tickets.every((ticket) => !ticket.valid)).toBe(true);
  expect((await call("getOrder", ref(next))).status).toBe("CONFIRMED");
});

test("purchases persist their entities in service-owned SQL tables", async () => {
  const clients: Keel[] = [];
  const database = async (service: string) => {
    const name = `cinema-${service}`;
    const client = await createKeel({
      name,
      user: name,
      token: credentials.services[name],
      url: environment.KEEL_URL,
    });
    clients.push(client);
    return client;
  };
  try {
    const booking = await database("booking");
    const payments = await database("payments");
    const notifications = await database("notifications");
    const show = await screening({ holdMs: 30_000 });
    expect(
      (
        await catalogDB.sql.query("SELECT id FROM screenings WHERE id=?", [
          show.id,
        ])
      ).rows,
    ).toHaveLength(1);
    const reserved = await reserve(show, "sql-tables");
    expect(
      (
        await booking.sql.query("SELECT status FROM orders WHERE id=?", [
          reserved.id,
        ])
      ).rows[0]?.status,
    ).toBe("HELD");
    expect(
      (
        await booking.sql.query("SELECT id FROM order_seats WHERE order_id=?", [
          reserved.id,
        ])
      ).rows,
    ).toHaveLength(1);
    const confirmed = await pay(reserved);
    expect(
      (
        await booking.sql.query("SELECT status FROM orders WHERE id=?", [
          reserved.id,
        ])
      ).rows[0]?.status,
    ).toBe("CONFIRMED");
    expect(
      (
        await booking.sql.query(
          "SELECT id FROM tickets WHERE order_id=? AND valid=1",
          [reserved.id],
        )
      ).rows,
    ).toHaveLength(1);
    expect(
      (
        await booking.sql.query(
          "SELECT status FROM payment_attempts WHERE order_id=?",
          [reserved.id],
        )
      ).rows[0]?.status,
    ).toBe("APPROVED");
    expect(
      (
        await payments.sql.query(
          "SELECT charge_count FROM payments WHERE id=?",
          [confirmed.paymentId],
        )
      ).rows[0]?.charge_count,
    ).toBe(1);
    const deliveries = await eventually(
      async () =>
        (
          await notifications.sql.query(
            "SELECT receipt_id FROM deliveries WHERE order_id=? AND delivered=1",
            [reserved.id],
          )
        ).rows,
      (rows) => rows.length === 1,
    );
    expect(deliveries[0]?.receipt_id).toBe(`receipt-${reserved.id}:confirmed`);
  } finally {
    await Promise.all(clients.map((client) => client.stop()));
  }
}, 20_000);

test("service restarts recover an authorized payment and preserve orders, tickets, and receipts", async () => {
  const show = await screening({ holdMs: 30_000 });
  const order = await reserve(show, "restart");
  await catalogDB.sql.query("UPDATE movies SET title = ? WHERE id = ?", [
    "Edited Orbit",
    "orbit",
  ]);
  await catalogDB.sql.query("DELETE FROM movies WHERE id = ?", ["midnight"]);
  await catalogDB.sql.query("UPDATE cinemas SET name = ? WHERE id = ?", [
    "Edited Downtown Cinema",
    "downtown",
  ]);
  await app.client(BookingServiceContract).beginPayment({
    order: ref(order),
    requestId: "restart-payment",
    mode: "duplicate_callback",
    delayMs: 500,
  });
  await stopServices(services);
  services = await startServices(environment, false);
  const catalog = await call<{ cinemas: Cinema[]; movies: Movie[] }>(
    "list",
    {},
  );
  expect(catalog.movies.find((movie) => movie.id === "orbit")?.title).toBe(
    "Edited Orbit",
  );
  expect(catalog.movies.some((movie) => movie.id === "midnight")).toBe(false);
  expect(catalog.cinemas.find((cinema) => cinema.id === "downtown")?.name).toBe(
    "Edited Downtown Cinema",
  );
  const confirmed = await eventually(
    () => call("getOrder", ref(order)),
    (next) => next.status === "CONFIRMED",
  );
  expect(confirmed.id).toBe(order.id);
  expect(confirmed.tickets).toHaveLength(1);
  const payment = await app
    .client(PaymentsServiceContract)
    .get({ paymentId: confirmed.paymentId });
  expect(payment.chargeCount).toBe(1);
  const before = confirmed.tickets[0]!.id;
  await stopServices(services);
  services = await startServices(environment, false);
  expect(
    (
      await catalogDB.sql.query("SELECT id FROM movies WHERE id = ?", [
        "midnight",
      ])
    ).rows,
  ).toHaveLength(0);
  expect((await call("getOrder", ref(order))).tickets[0]!.id).toBe(before);
  const receipts = await eventually(
    () =>
      call<{ event: { kind: string } }[]>("receipts", {
        orderId: order.id,
        customerId: order.customerId,
      }),
    (list) => list.some((item) => item.event.kind === "confirmed"),
  );
  expect(
    receipts.filter((item) => item.event.kind === "confirmed"),
  ).toHaveLength(1);
}, 25_000);

(process.env.KEEL_SQL_ENGINE === "dqlite" ? test : test.skip)(
  "a killed core preserves committed SQL and the remaining quorum can complete a purchase",
  async () => {
    expect(coreNodes).toHaveLength(3);
    const show = await screening({ holdMs: 30_000 });
    await catalogDB.sql.query("UPDATE movies SET title = ? WHERE id = ?", [
      "Survives a core crash",
      "orbit",
    ]);
    const exited = once(coreNodes[0]!, "exit");
    coreNodes[0]!.kill("SIGKILL");
    await exited;
    const deadline = Date.now() + 15_000;
    for (;;) {
      try {
        const result = await catalogDB.sql.query<{ title: string }>(
          "SELECT title FROM movies WHERE id = ?",
          ["orbit"],
        );
        expect(result.rows[0]?.title).toBe("Survives a core crash");
        break;
      } catch (error) {
        if (Date.now() >= deadline) throw error;
        await Bun.sleep(100);
      }
    }
    // Leadership changes can reject a request. Reuse the purchase's idempotency
    // keys when the client retries; the SQL SDK never replays transactions.
    const order = await eventually(
      () => reserve(show, "after-core-crash", ["B1"], "crash-hold"),
      (order) => order.status === "HELD",
      15_000,
    );
    const confirmed = await eventually(
      () => pay(order),
      (order) => order.status === "CONFIRMED",
      15_000,
    );
    expect(confirmed.tickets).toHaveLength(1);
  },
  25_000,
);
