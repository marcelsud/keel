import { randomUUID } from "node:crypto";
import { connect, type Screening } from "../packages/shared-ts/index.js";
import { readCredentials } from "../tools/dev.js";

type Order = {
  id: string;
  screeningId: string;
  customerId: string;
  status: string;
  totalMinor: number;
  currency: string;
  refundStatus: string;
  tickets: { id: string; seatId: string; valid: boolean }[];
};
const credentials = await readCredentials();
process.env.KEEL_USER = "cinema-client";
process.env.KEEL_TOKEN = credentials.services["cinema-client"];
const app = await connect("cinema-demo-client");
const call = <T>(action: string, input: unknown) =>
  app.call<T>("cinema-checkout", action, input);

async function waitFor(
  ref: object,
  ready: (order: Order) => boolean,
): Promise<Order> {
  for (let i = 0; i < 100; i++) {
    const order = await call<Order>("getOrder", ref);
    if (ready(order)) return order;
    await Bun.sleep(100);
  }
  throw new Error(
    "purchase did not settle; retry getOrder with the same order reference",
  );
}

try {
  const id = `demo-${randomUUID()}`;
  const existing = await app.call<Screening[]>(
    "cinema-catalog",
    "listScreenings",
    {},
  );
  const startsAtMs = Math.max(
    Date.now() + 2 * 60 * 60_000,
    ...existing
      .filter(
        (show) =>
          show.cinemaId === "riverside" && show.auditoriumId === "hall-2",
      )
      .map((show) => show.endsAtMs + 60_000),
  );
  await app.call("cinema-catalog", "schedule", {
    id,
    cinemaId: "riverside",
    auditoriumId: "hall-2",
    movieId: "orbit",
    startsAtMs,
    basePriceMinor: 1200,
    currency: "USD",
  });
  const held = await call<Order>("reserve", {
    screeningId: id,
    customerId: "ada",
    requestId: `${id}-hold`,
    seatIds: ["A1", "A2"],
  });
  const ref = { screeningId: id, customerId: "ada", orderId: held.id };
  console.log("JSON checkout → protobuf Booking: seats A1 and A2 held", ref);
  await call("pay", {
    ...ref,
    requestId: `${id}-pay`,
    mode: "duplicate_callback",
    delayMs: 200,
  });
  const confirmed = await waitFor(ref, (order) => order.status === "CONFIRMED");
  console.log("Protobuf Payments → durable JSON events: purchase confirmed");
  console.log(JSON.stringify(confirmed, null, 2));
  if (process.argv.includes("--cancel")) {
    await call("cancel", ref);
    const canceled = await waitFor(
      ref,
      (order) => order.refundStatus === "REFUNDED",
    );
    console.log(
      "Full cancellation: tickets invalidated, seats released, payment refunded",
    );
    console.log(JSON.stringify(canceled, null, 2));
  }
  for (let i = 0; i < 100; i++) {
    const receipts = await call<{ event: { kind: string } }[]>("receipts", {
      orderId: held.id,
      customerId: "ada",
    });
    if (receipts.some((receipt) => receipt.event.kind === "confirmed")) {
      console.log("Receipts:", receipts);
      break;
    }
    if (i === 99)
      throw new Error(
        "purchase succeeded, but its notification has not arrived yet",
      );
    await Bun.sleep(100);
  }
} finally {
  await app.stop();
}
