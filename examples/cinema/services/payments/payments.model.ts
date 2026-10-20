import { type MessageShape } from "@bufbuild/protobuf";
import { type PaymentSchema } from "../../contracts/generated/typescript/cinema/payments/v1/payments_pb.js";

export type Payment = Omit<MessageShape<typeof PaymentSchema>, "$typeName">;
export type PaymentEvent = {
  id: string;
  paymentId: string;
  screeningId: string;
  orderId: string;
  customerId: string;
  copies: number;
};
export type State = {
  payments: Record<string, Payment>;
  outbox: Record<string, PaymentEvent>;
};

export function queuePaymentEvent(state: State, payment: Payment): void {
  if (payment.mode === "lost_callback") return;
  const id = `${payment.id}:${payment.status}`;
  state.outbox[id] = {
    id,
    paymentId: payment.id,
    screeningId: payment.screeningId!,
    orderId: payment.orderId!,
    customerId: payment.customerId!,
    copies: payment.mode === "duplicate_callback" ? 2 : 1,
  };
}
