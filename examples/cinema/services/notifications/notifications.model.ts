import { type BookingEvent } from "../../packages/shared-ts/index.js";
export type Delivery = {
  event: BookingEvent;
  attempts: number;
  delivered: boolean;
  receiptId: string;
};
export type NotificationState = { deliveries: Record<string, Delivery> };
