import { type MessageShape } from "@bufbuild/protobuf";
import { type ChargeRequestSchema } from "../../contracts/generated/typescript/cinema/payments/v1/payments_pb.js";
export type ChargePaymentDto = MessageShape<typeof ChargeRequestSchema>;
