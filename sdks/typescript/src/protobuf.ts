import {
  create,
  createFileRegistry,
  fromBinary,
  toBinary,
  type DescService,
  type Message,
  type MessageInitShape,
  type MessageShape,
} from "@bufbuild/protobuf";
import { FileDescriptorSetSchema } from "@bufbuild/protobuf/wkt";
import { ServiceError } from "@nats-io/services";
import type { HandlerContext } from "./index.js";

const prefix = "keel.v1";
const encoder = new TextEncoder();
const qualifiedName = /^[a-zA-Z_]\w*(?:\.[a-zA-Z_]\w*)+$/;

export type ContractState = "active" | "deprecated" | "retired";
export interface Contract<S extends DescService = DescService> {
  service: S;
  schema: string;
}
export interface ContractOptions {
  state?: ContractState;
  replacement?: string;
}
export interface Schema {
  service: string;
  sha256: string;
  state: ContractState;
  replacement?: string;
  descriptorSet: string;
}

export type ServiceImplementation<S extends DescService> = {
  [K in keyof S["method"]]: S["method"][K]["methodKind"] extends "unary"
    ? (
        ctx: HandlerContext,
        input: MessageShape<S["method"][K]["input"]>,
      ) =>
        | MessageInitShape<S["method"][K]["output"]>
        | Promise<MessageInitShape<S["method"][K]["output"]>>
    : never;
};

export type ServiceClient<S extends DescService> = {
  [K in keyof S["method"]]: S["method"][K]["methodKind"] extends "unary"
    ? (
        input: MessageInitShape<S["method"][K]["input"]>,
      ) => Promise<MessageShape<S["method"][K]["output"]>>
    : never;
};

export interface BinaryEndpoint {
  subject: string;
  handler(data: Uint8Array): Promise<Uint8Array>;
  metadata?: Record<string, string>;
}

function decode(value: string): Uint8Array {
  return Uint8Array.from(atob(value), (character) => character.charCodeAt(0));
}

async function digest(data: Uint8Array): Promise<string> {
  const bytes = new Uint8Array(
    await crypto.subtle.digest("SHA-256", new Uint8Array(data)),
  );
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join(
    "",
  );
}

export function schemaSubject(service: string): string {
  if (!qualifiedName.test(service)) throw new Error("invalid contract name");
  return `${prefix}.schema.${service}`;
}

function rpcSubject(service: string, method: string): string {
  return `${prefix}.rpc.${service}.${method}`;
}
function endpointName(kind: string, subject: string): string {
  return `${kind}_${btoa(subject).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "")}`;
}

function descriptor(data: Uint8Array, service: string): DescService {
  const registry = createFileRegistry(
    fromBinary(FileDescriptorSetSchema, data),
  );
  const result = registry.getService(service);
  if (!result) throw new Error(`contract does not contain service ${service}`);
  if (result.methods.some((method) => method.methodKind !== "unary"))
    throw new Error("streaming RPCs are not supported");
  return result;
}

export function serviceEndpoints<S extends DescService>(
  contract: Contract<S>,
  implementation: ServiceImplementation<S> | undefined,
  options: ContractOptions,
  context: HandlerContext,
): Map<string, BinaryEndpoint> {
  const service = contract.service;
  schemaSubject(service.typeName);
  const data = decode(contract.schema);
  const embedded = descriptor(data, service.typeName);
  const state =
    options.state ??
    (service.proto.options?.deprecated ? "deprecated" : "active");
  const replacement = options.replacement;
  if (!["active", "deprecated", "retired"].includes(state))
    throw new Error("invalid contract state");
  if (replacement !== undefined && !qualifiedName.test(replacement))
    throw new Error("replacement must be a qualified contract name");
  if (embedded.methods.length !== service.methods.length)
    throw new Error("contract method mismatch");
  const metadata = {
    contract: service.typeName,
    state,
    replacement: replacement ?? "",
  };
  const routes = new Map<string, BinaryEndpoint>();
  routes.set(endpointName("schema", service.typeName), {
    subject: schemaSubject(service.typeName),
    metadata,
    handler: async () =>
      encoder.encode(
        JSON.stringify({
          service: service.typeName,
          sha256: await digest(data),
          state,
          replacement,
          descriptorSet: contract.schema,
        } satisfies Schema),
      ),
  });
  for (const method of service.methods) {
    const declared = embedded.methods.find(
      (candidate) => candidate.name === method.name,
    );
    if (
      !declared ||
      declared.input.typeName !== method.input.typeName ||
      declared.output.typeName !== method.output.typeName ||
      method.methodKind !== "unary"
    ) {
      throw new Error("contract method mismatch");
    }
    const handler =
      implementation?.[method.localName as keyof ServiceImplementation<S>];
    if (state !== "retired" && typeof handler !== "function")
      throw new Error(`missing handler for ${method.name}`);
    routes.set(endpointName("rpc", `${service.typeName}.${method.name}`), {
      subject: rpcSubject(service.typeName, method.name),
      metadata,
      handler: async (bytes) => {
        if (state === "retired")
          throw new ServiceError(
            410,
            `API retired; replacement: ${replacement ?? ""}`,
          );
        let input;
        try {
          input = fromBinary(method.input, bytes);
        } catch {
          throw new ServiceError(400, "invalid protobuf request");
        }
        // Reflection erases method-specific types here; the public mapped type checks both sides.
        const invoke = handler as unknown as (
          ctx: HandlerContext,
          input: Message,
        ) => Promise<Record<string, unknown>> | Record<string, unknown>;
        return toBinary(
          method.output,
          create(method.output, await invoke(context, input)),
        );
      },
    });
  }
  return routes;
}

export function protobufClient<S extends DescService>(
  contract: Contract<S>,
  request: (subject: string, data: Uint8Array) => Promise<Uint8Array>,
): ServiceClient<S> {
  schemaSubject(contract.service.typeName);
  const result: Record<string, unknown> = Object.create(null);
  for (const method of contract.service.methods) {
    if (method.methodKind !== "unary")
      throw new Error("streaming RPCs are not supported");
    result[method.localName] = async (input: Record<string, unknown>) => {
      const data = toBinary(method.input, create(method.input, input));
      return fromBinary(
        method.output,
        await request(rpcSubject(contract.service.typeName, method.name), data),
      );
    };
  }
  return result as ServiceClient<S>;
}

export async function validateSchema(
  value: Schema,
  service: string,
): Promise<Schema> {
  if (
    value.service !== service ||
    !["active", "deprecated", "retired"].includes(value.state)
  )
    throw new Error("invalid contract metadata");
  if (value.replacement !== undefined && !qualifiedName.test(value.replacement))
    throw new Error("invalid replacement contract");
  const data = decode(value.descriptorSet);
  if ((await digest(data)) !== value.sha256)
    throw new Error("contract checksum mismatch");
  descriptor(data, service);
  return value;
}
