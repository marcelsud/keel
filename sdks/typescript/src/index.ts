import { connect, type NatsConnection } from "@nats-io/transport-node";
import process from "node:process";
import { createHttpRouter, type HttpAdapter, type HttpHandler, type HttpMethod, type HttpServer } from "./http.js";
export type { HttpAdapter, HttpContext, HttpHandler, HttpMethod, HttpQuery, HttpRoute, ActionRoute, HttpServer, ListenOptions } from "./http.js";
import { SQL } from "./sql.js";
export {
  SQL,
  SQLCommitUnknownError,
  type SQLResult,
  type SQLParameter,
} from "./sql.js";
export type { SQLTransaction } from "./sql.js";
import {
  ServiceError,
  Svcm,
  type Service,
  type ServiceMsg,
} from "@nats-io/services";
import { jetstream, type ConsumerMessages } from "@nats-io/jetstream";
import type { DescService } from "@bufbuild/protobuf";
import { schema, ValidationError } from "./schema.js";
import {
  protobufClient,
  schemaSubject,
  serviceEndpoints,
  validateSchema,
  type BinaryEndpoint,
  type Contract,
  type ContractOptions,
  type Schema,
  type ServiceClient,
  type ServiceImplementation,
} from "./protobuf.js";

export { ServiceError, schema, ValidationError };
export type { Infer, Validator } from "./schema.js";
export type {
  Contract,
  ContractOptions,
  ContractState,
  Schema,
  ServiceClient,
  ServiceImplementation,
} from "./protobuf.js";

const prefix = "keel.v1";
const identifier = /^[a-zA-Z0-9_-]+$/;

function name(value: string): void {
  if (!identifier.test(value))
    throw new ValidationError("invalid service or action name");
}

function nonempty(value: string): void {
  if (typeof value !== "string" || !value.trim())
    throw new ValidationError("name must not be empty");
}

function json(value: unknown): string {
  const result = JSON.stringify(value);
  if (result === undefined)
    throw new ValidationError("value must be JSON serializable");
  return result;
}

export interface Options {
  http?: HttpAdapter;
  url?: string;
  name: string;
  version?: string;
  token?: string;
  user?: string;
  timeout?: number;
}

type Request = <T>(operation: string, input: unknown) => Promise<T>;

export class KV {
  constructor(
    private request: Request,
    private scope = "",
  ) {}
  namespace(namespace: string): KV {
    nonempty(namespace);
    return new KV(this.request, namespace);
  }
  async get<T = unknown>(key: string): Promise<T | undefined> {
    const result = await this.request<{ found: boolean; value?: T }>("kv_get", {
      namespace: this.scope,
      key,
    });
    return result.found ? result.value : undefined;
  }
  async set(key: string, value: unknown): Promise<void> {
    json(value);
    await this.request("kv_set", { namespace: this.scope, key, value });
  }
  async getEntry<T = unknown>(
    key: string,
  ): Promise<{ value: T; revision: number } | undefined> {
    const result = await this.request<{
      found: boolean;
      value: T;
      revision: number;
    }>("kv_get", { namespace: this.scope, key });
    return result.found
      ? { value: result.value, revision: result.revision }
      : undefined;
  }
  compareAndSet(
    key: string,
    value: unknown,
    revision: number,
  ): Promise<number> {
    json(value);
    if (!Number.isSafeInteger(revision) || revision < 0)
      throw new ValidationError("revision must be a nonnegative safe integer");
    return this.request("kv_cas", {
      namespace: this.scope,
      key,
      value,
      revision,
    });
  }
  delete(key: string): Promise<boolean> {
    return this.request("kv_delete", { namespace: this.scope, key });
  }
}

export class Collections {
  constructor(private request: Request) {}
  create(
    collection: string,
    id: string,
    document: Record<string, unknown>,
    options: { ifNeverExisted?: boolean } = {},
  ): Promise<string> {
    nonempty(id);
    return this.request("collection_insert", {
      collection,
      id,
      document,
      ifNeverExisted: options.ifNeverExisted,
    });
  }
  list<T extends object = Record<string, unknown>>(
    collection: string,
  ): Promise<(T & { id: string })[]> {
    return this.request("collection_list", { collection });
  }
  insert(
    collection: string,
    document: Record<string, unknown>,
  ): Promise<string> {
    return this.request("collection_insert", { collection, document });
  }
  get<T extends object = Record<string, unknown>>(
    collection: string,
    id: string,
  ): Promise<(T & { id: string }) | null> {
    return this.request("collection_get", { collection, id });
  }
  update(
    collection: string,
    id: string,
    document: Record<string, unknown>,
  ): Promise<boolean> {
    return this.request("collection_update", { collection, id, document });
  }
  delete(collection: string, id: string): Promise<boolean> {
    return this.request("collection_delete", { collection, id });
  }
}

export interface HandlerContext {
  sql: SQL;
  kv: KV;
  collection: Collections;
  pubsub: { publish(topic: string, payload: unknown): Promise<void> };
  queue: { enqueue(queue: string, payload: unknown): Promise<void> };
  call<T = unknown>(
    service: string,
    action: string,
    input: unknown,
  ): Promise<T>;
  client<S extends DescService>(contract: Contract<S>): ServiceClient<S>;
  log: Pick<Console, "info" | "error">;
}

type Handler = (
  ctx: HandlerContext,
  payload: unknown,
) => unknown | Promise<unknown>;
type Binding = {
  kind: "worker" | "subscription";
  topic: string;
  name: string;
  handler: Handler;
};

export class Keel implements HandlerContext {
  private router = createHttpRouter();
  private httpServer?: HttpServer;
  private requests = new Set<Promise<Response>>();
  private closing = false;
  readonly route: typeof this.router.route = (method: HttpMethod, path: string, definition: any) => {
    this.declaring();
    this.router.route(method, path, definition);
  };
  readonly fetch: HttpHandler = (request, context) => {
    if (this.closing) return Promise.resolve(new Response("Service unavailable", { status: 503 }));
    const pending = this.router.fetch(request, context);
    this.requests.add(pending);
    return pending.finally(() => this.requests.delete(pending));
  };
  get http(): HttpServer | undefined { return this.httpServer; }
  readonly sql: SQL;
  readonly schema = schema;
  readonly kv: KV;
  readonly collection: Collections;
  readonly pubsub: HandlerContext["pubsub"];
  readonly queue: HandlerContext["queue"];
  readonly log = console;
  private endpoints = new Map<string, BinaryEndpoint>();
  private bindings: Binding[] = [];
  private nativeService?: Service;
  private messages: ConsumerMessages[] = [];
  private deliveries: Promise<void>[] = [];
  private active = new Set<Promise<void>>();
  private starting?: Promise<void>;
  private stopping?: Promise<void>;
  private stopped = false;
  private readonly onSignal = (): void => {
    void this.stop().catch((error) => {
      this.log.error("shutdown failed", error);
      process.exitCode = 1;
    });
  };

  constructor(
    private nc: NatsConnection,
    private options: Options,
  ) {
    this.sql = new SQL(options.name, (subject, input) =>
      this.request(subject, input),
    );
    const request: Request = (operation, input) =>
      this.request(`${prefix}.core.${operation}`, input);
    this.kv = new KV(request);
    this.collection = new Collections(request);
    this.pubsub = {
      publish: async (topic, payload) => {
        json(payload);
        await request("publish", { topic, payload });
      },
    };
    this.queue = {
      enqueue: async (topic, payload) => {
        json(payload);
        await request("enqueue", { topic, payload });
      },
    };
  }

  private declaring(): void {
    if (this.starting || this.closing)
      throw new Error("declare actions, consumers and routes before start");
  }

  action<Input, Result>(definition: {
    name: string;
    args?: { parse(value: unknown): Input };
    handler: (ctx: HandlerContext, input: Input) => Result | Promise<Result>;
  }): (input: Input) => Promise<Awaited<Result>> {
    this.declaring();
    name(definition.name);
    if (this.endpoints.has(definition.name))
      throw new Error(`duplicate action: ${definition.name}`);
    const invoke = async (payload: unknown) => {
      let input = payload as Input;
      if (definition.args) {
        try {
          input = definition.args.parse(payload);
        } catch (error) {
          throw new ValidationError(
            error instanceof Error ? error.message : "invalid input",
          );
        }
      }
      return definition.handler(this, input);
    };
    this.endpoints.set(definition.name, {
      subject: `${prefix}.actions.${this.options.name}.${definition.name}`,
      handler: async (data) => {
        let input;
        try {
          input = JSON.parse(new TextDecoder().decode(data));
        } catch {
          throw new ValidationError("input must be JSON");
        }
        return new TextEncoder().encode(json(await invoke(input)));
      },
    });
    return (input) =>
      this.call<Awaited<Result>>(this.options.name, definition.name, input);
  }

  service<S extends DescService>(
    contract: Contract<S>,
    implementation: ServiceImplementation<S> | undefined,
    options: ContractOptions = {},
  ): void {
    this.declaring();
    const routes = serviceEndpoints(contract, implementation, options, this);
    for (const name of routes.keys())
      if (this.endpoints.has(name))
        throw new Error(`duplicate contract: ${contract.service.typeName}`);
    for (const [name, route] of routes) this.endpoints.set(name, route);
  }

  client<S extends DescService>(contract: Contract<S>): ServiceClient<S> {
    return protobufClient(contract, (subject, data) =>
      this.requestBytes(subject, data),
    );
  }

  async fetchContract(service: string): Promise<Schema> {
    return validateSchema(
      await this.request<Schema>(schemaSubject(service), null),
      service,
    );
  }

  subscription<Payload>(
    topic: string,
    handler: (
      ctx: HandlerContext,
      payload: Payload,
    ) => unknown | Promise<unknown>,
    options: { name: string },
  ): void {
    this.bind({
      kind: "subscription",
      topic,
      name: options?.name,
      handler: handler as Handler,
    });
  }

  worker<Payload>(
    queue: string,
    handler: (
      ctx: HandlerContext,
      payload: Payload,
    ) => unknown | Promise<unknown>,
  ): void {
    this.bind({
      kind: "worker",
      topic: queue,
      name: queue,
      handler: handler as Handler,
    });
  }

  private bind(binding: Binding): void {
    this.declaring();
    nonempty(binding.topic);
    nonempty(binding.name);
    if (
      this.bindings.some(
        (b) =>
          b.kind === binding.kind &&
          b.topic === binding.topic &&
          b.name === binding.name,
      )
    ) {
      throw new Error("duplicate consumer");
    }
    this.bindings.push(binding);
  }

  call<T = unknown>(
    service: string,
    action: string,
    input: unknown,
  ): Promise<T> {
    name(service);
    name(action);
    return this.request(`${prefix}.actions.${service}.${action}`, input);
  }

  private async request<T>(subject: string, input: unknown): Promise<T> {
    const data = await this.requestBytes(
      subject,
      new TextEncoder().encode(json(input)),
    );
    return JSON.parse(new TextDecoder().decode(data)) as T;
  }

  private async requestBytes(
    subject: string,
    data: Uint8Array,
  ): Promise<Uint8Array> {
    if (this.nc.isClosed()) throw new Error("app is stopped");
    const message = await this.nc.request(subject, data, {
      timeout: this.options.timeout ?? 5000,
    });
    const error = ServiceError.toServiceError(message);
    if (error) throw error;
    return message.data;
  }

  start({ signals = true, http }: { signals?: boolean; http?: HttpAdapter } = {}): Promise<void> {
    if (this.closing) return Promise.reject(new Error("app is stopped"));
    if (this.options.http && http) return Promise.reject(new Error("Configure HTTP in either createKeel() or start(), not both"));
    if (!this.starting) {
      if (signals) {
        process.once("SIGINT", this.onSignal);
        process.once("SIGTERM", this.onSignal);
      }
      this.starting = this.initialize(http ?? this.options.http).catch((error) => {
        this.removeSignalHandlers();
        this.starting = undefined;
        throw error;
      });
    }
    return this.starting;
  }

  private removeSignalHandlers(): void {
    process.removeListener("SIGINT", this.onSignal);
    process.removeListener("SIGTERM", this.onSignal);
  }

  private async initialize(http?: HttpAdapter): Promise<void> {
    try {
      this.nativeService = await new Svcm(this.nc).add({
        name: this.options.name,
        version: this.options.version ?? "0.1.0",
      });
      for (const [name, route] of this.endpoints) {
        this.nativeService.addEndpoint(name, {
          subject: route.subject,
          metadata: route.metadata,
          handler: (error, message) => {
            if (error) {
              this.log.error("action subscription failed", error);
              return;
            }
            if (this.stopped) {
              message.respondError(503, "app is stopping");
              return;
            }
            const task = this.handle(message, route.handler);
            this.active.add(task);
            void task.finally(() => this.active.delete(task));
          },
        });
      }
      const js = jetstream(this.nc);
      for (const binding of this.bindings) {
        const info = await this.request<{ stream: string; consumer: string }>(
          `${prefix}.core.consumer`,
          {
            kind: binding.kind,
            topic: binding.topic,
            name: binding.name,
          },
        );
        const consumer = await js.consumers.get(info.stream, info.consumer);
        this.messages.push(
          await consumer.consume({ max_messages: 1, expires: 1000 }),
        );
      }
      await this.nc.flush();
      if (http) this.httpServer = await http(this.fetch);
      if (this.closing) throw new Error("app is stopped");
      this.deliveries = this.messages.map((messages, index) =>
        this.consume(messages, this.bindings[index]!.handler),
      );
    } catch (error) {
      await this.httpServer?.stop();
      this.httpServer = undefined;
      await Promise.all(this.messages.map((messages) => messages.close()));
      this.messages = [];
      await this.nativeService?.stop();
      this.nativeService = undefined;
      throw error;
    }
  }

  private async handle(
    message: ServiceMsg,
    handler: BinaryEndpoint["handler"],
  ): Promise<void> {
    try {
      message.respond(await handler(message.data));
    } catch (error) {
      if (error instanceof ValidationError)
        message.respondError(400, error.message);
      else if (error instanceof ServiceError)
        message.respondError(error.code, error.message);
      else {
        this.log.error("action failed", error);
        message.respondError(500, "handler failed");
      }
    }
  }

  private async consume(
    messages: ConsumerMessages,
    handler: Handler,
  ): Promise<void> {
    try {
      for await (const message of messages) {
        if (this.stopped) {
          message.nak();
          continue;
        }
        const heartbeat = setInterval(() => message.working(), 10_000);
        try {
          await handler(this, message.json());
          await message.ackAck();
        } catch (error) {
          this.log.error("delivery failed", error);
          message.nak(1000);
        } finally {
          clearInterval(heartbeat);
        }
      }
    } catch (error) {
      if (!this.stopped) this.log.error("consumer stopped", error);
    }
  }

  stop(): Promise<void> {
    this.closing = true;
    this.removeSignalHandlers();
    this.stopping ??= (async () => {
      try {
        await this.starting?.catch(() => {});
        const [http] = await Promise.allSettled([Promise.resolve().then(() => this.httpServer?.stop())]);
        await Promise.allSettled(this.requests);
        this.stopped = true;
        await this.nativeService?.stop();
        await Promise.all(this.messages.map((messages) => messages.close()));
        await Promise.all(this.deliveries);
        await Promise.all(this.active);
        await this.nc.drain();
        if (http.status === "rejected") throw http.reason;
      } finally {
        await this.nc.close();
      }
    })();
    return this.stopping;
  }
}

export async function createKeel(options: Options): Promise<Keel> {
  name(options.name);
  if (options.name === "keel-core")
    throw new ValidationError("reserved service name");
  const nc = await connect({
    servers: (options.url ?? "nats://127.0.0.1:4222")
      .split(",")
      .map((url) => url.trim()),
    token: options.user ? undefined : options.token,
    user: options.user,
    pass: options.user ? options.token : undefined,
    inboxPrefix: options.user ? `keel.inbox.${options.user}` : undefined,
    name: options.name,
    timeout: 5000,
  });
  return new Keel(nc, options);
}
