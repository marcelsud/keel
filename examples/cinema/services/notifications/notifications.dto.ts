import { schema, type Infer } from "@marcelsud/keel.ts";
import { fail, type BookingEvent } from "../../packages/shared-ts/index.js";
export const receiptQuery = schema.object({
  orderId: schema.string(),
  customerId: schema.string(),
});
export type ReceiptQueryDto = Infer<typeof receiptQuery>;
export function validateBookingEvent(event: BookingEvent): void {
  if (
    !event ||
    typeof event.id !== "string" ||
    !/^[a-f0-9]{32}:(confirmed|canceled|expired|refunded)(:[a-f0-9]{32}-[1-5])?$/.test(
      event.id,
    ) ||
    !event.id.startsWith(event.orderId + ":" + event.kind) ||
    !event.customerId ||
    !event.screeningId
  )
    fail(400, "invalid booking event");
}
