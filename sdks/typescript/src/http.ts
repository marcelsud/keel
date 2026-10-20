import { ServiceError } from "@nats-io/services";
import { ValidationError } from "./schema.js";

type Parser<T> = { parse(value: unknown, path?: string): T };

export type HttpMethod = "GET" | "HEAD" | "POST" | "PUT" | "PATCH" | "DELETE" | "OPTIONS";
type MaybePromise<T> = T | Promise<T>;
export type HttpQuery = Record<string, string | string[]>;

export interface HttpContext<Body = unknown, Params = Record<string, string>, Query = HttpQuery> {
  request: Request;
  params: Params;
  query: Query;
  body: Body;
  native?: unknown;
}

export type HttpHandler = (request: Request, context?: { native?: unknown }) => Promise<Response>;
export interface HttpServer {
  readonly url: URL;
  stop(): Promise<void>;
}
export type HttpAdapter = (handle: HttpHandler) => Promise<HttpServer>;
export interface ListenOptions {
  port?: number;
  hostname?: string;
}

interface RouteOptions<Body, Params, Query, Result> {
  body?: Parser<Body>;
  params?: Parser<Params>;
  query?: Parser<Query>;
  status?: number;
  output?: (result: Awaited<Result>, context: HttpContext<Body, Params, Query>) => MaybePromise<unknown>;
}

export type HttpRoute<Body = unknown, Params = Record<string, string>, Query = HttpQuery, Result = unknown> =
  RouteOptions<Body, Params, Query, Result> & {
    handler: (context: HttpContext<Body, Params, Query>) => MaybePromise<Result>;
    action?: never;
    input?: never;
  };

export type ActionRoute<Input, Result, Body = unknown, Params = Record<string, string>, Query = HttpQuery> =
  RouteOptions<Body, Params, Query, Result> & {
    action: (input: Input) => MaybePromise<Result>;
    input?: (context: HttpContext<Body, Params, Query>) => MaybePromise<NoInfer<Input>>;
    handler?: never;
  };

// Generic route types are erased only inside the dispatcher.
type ErasedContext = HttpContext<any, any, any>;
type Definition = RouteOptions<any, any, any, any> & {
  handler?: (context: ErasedContext) => unknown;
  action?: (input: any) => unknown;
  input?: (context: ErasedContext) => unknown;
};
interface RegisteredRoute {
  method: HttpMethod;
  segments: string[];
  shape: string;
  options: Definition;
}

const methods: HttpMethod[] = ["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"];

function parse<T>(validator: Parser<T>, value: unknown, path: string): T {
  try { return validator.parse(value, path); }
  catch (error) { throw new ValidationError(error instanceof Error ? error.message : String(error)); }
}

async function readJson(request: Request): Promise<unknown> {
  if (request.body === null) return {};
  try { return await request.json(); }
  catch { throw new ValidationError("Request body must contain valid JSON"); }
}

function responseFor(value: unknown, status?: number): Response {
  if (value instanceof Response) return value;
  if (status === 204 || status === 205 || status === 304) return new Response(null, { status });
  if (value === undefined) return new Response(null, { status: status ?? 204 });
  const data = JSON.stringify(value, (_key, entry: unknown) => {
    if (entry === undefined || typeof entry === "function" || typeof entry === "symbol" || typeof entry === "bigint" || (typeof entry === "number" && !Number.isFinite(entry))) {
      throw new TypeError("HTTP responses must contain JSON values");
    }
    return entry;
  });
  return new Response(data, {
    status: status ?? 200,
    headers: { "Content-Type": "application/json; charset=utf-8" },
  });
}

export function createHttpRouter() {
  const routes: RegisteredRoute[] = [];

  function route<Body = unknown, Params = Record<string, string>, Query = HttpQuery, Result = unknown>(
    method: HttpMethod, path: string,
    handler: (context: HttpContext<Body, Params, Query>) => MaybePromise<Result>,
  ): void;
  function route<Body = unknown, Params = Record<string, string>, Query = HttpQuery, Result = unknown>(
    method: HttpMethod, path: string, options: HttpRoute<Body, Params, Query, Result>,
  ): void;
  function route<Input, Result, Body = unknown, Params = Record<string, string>, Query = HttpQuery>(
    method: HttpMethod, path: string, options: ActionRoute<Input, Result, Body, Params, Query>,
  ): void;
  function route(method: HttpMethod, path: string, definition: Definition | Definition["handler"]): void {
    if (!methods.includes(method)) throw new Error(`Unsupported HTTP method: ${method}`);
    if (typeof path !== "string" || !path.startsWith("/") || /[?#*]/.test(path) || path.includes("//")) {
      throw new Error("Routes require an absolute path with literal segments or :parameters");
    }
    if (new URL(path, "http://localhost").pathname !== path) {
      throw new Error("Route paths must use URL-encoded literals without dot segments or backslashes");
    }
    const segments = path.replace(/\/$/, "").split("/").slice(1);
    const names = new Set<string>();
    for (const segment of segments) {
      if (!segment.startsWith(":")) continue;
      const name = segment.slice(1);
      if (!/^[A-Za-z_]\w*$/.test(name) || names.has(name)) throw new Error("Invalid or repeated route parameter");
      names.add(name);
    }
    const shape = segments.map((part) => part.startsWith(":") ? ":" : part).join("/");
    if (routes.some((entry) => entry.method === method && entry.shape === shape)) {
      throw new Error(`Duplicate route: ${method} ${path}`);
    }
    const options = typeof definition === "function" ? { handler: definition } : definition;
    if (!options || (typeof options.handler === "function") === (typeof options.action === "function")) {
      throw new Error("A route requires exactly one handler or action");
    }
    if (options.input && !options.action) throw new Error("Route input requires an action");
    if (options.status !== undefined && (!Number.isInteger(options.status) || options.status < 200 || options.status > 599)) {
      throw new Error("Route status must be an integer between 200 and 599");
    }
    routes.push({ method, segments, shape, options: { ...options } });
    routes.sort((a, b) => {
      for (let index = 0; index < Math.min(a.segments.length, b.segments.length); index++) {
        const difference = Number(a.segments[index].startsWith(":")) - Number(b.segments[index].startsWith(":"));
        if (difference) return difference;
      }
      return 0;
    });
  }

  const dispatch: HttpHandler = async (request, supplied) => {
    let presenting = false;
    try {
      const url = new URL(request.url);
      const segments = url.pathname.replace(/\/$/, "").split("/").slice(1);
      let decoded: string[];
      try { decoded = segments.map(decodeURIComponent); }
      catch { throw new ValidationError("Invalid path encoding"); }
      const matches = routes.filter((entry) => entry.segments.length === segments.length && entry.segments.every(
        (part, index) => part.startsWith(":") ? segments[index] !== "" : part === segments[index],
      ));
      if (!matches.length) return new Response("Not found", { status: 404 });
      const matched = matches.find((entry) => entry.method === request.method)
        ?? (request.method === "HEAD" ? matches.find((entry) => entry.method === "GET") : undefined);
      if (!matched) {
        const allowed = new Set(matches.map((entry) => entry.method));
        if (allowed.has("GET")) allowed.add("HEAD");
        allowed.add("OPTIONS");
        const headers = { Allow: methods.filter((method) => allowed.has(method)).join(", ") };
        return request.method === "OPTIONS"
          ? new Response(null, { status: 204, headers })
          : new Response("Method not allowed", { status: 405, headers });
      }
      const params: Record<string, string> = Object.create(null);
      matched.segments.forEach((part, index) => { if (part.startsWith(":")) params[part.slice(1)] = decoded[index]; });
      const query: HttpQuery = Object.create(null);
      for (const key of new Set(url.searchParams.keys())) {
        const values = url.searchParams.getAll(key);
        query[key] = values.length === 1 ? values[0] : values;
      }
      const options = matched.options;
      const context: ErasedContext = {
        request, params, query, body: undefined, native: supplied?.native,
      };
      if (options.params) context.params = parse(options.params, params, "params");
      if (options.query) context.query = parse(options.query, query, "query");
      if (options.body) context.body = parse(options.body, await readJson(request), "body");
      let result: unknown;
      if (options.action) {
        let input: unknown;
        try {
          if (options.input) input = await options.input(context);
          else {
            if (!options.body) context.body = await readJson(request);
            input = context.body;
          }
        } catch (error) {
          if (error instanceof SyntaxError) throw new ValidationError("Invalid request input");
          throw error;
        }
        result = await options.action(input);
      } else result = await options.handler!(context);
      presenting = true;
      if (options.output) result = await options.output(result, context);
      return responseFor(result, options.status);
    } catch (error) {
      if (!presenting && error instanceof ValidationError) return Response.json({ error: error.message }, { status: 400 });
      if (!presenting && error instanceof ServiceError && error.code >= 400 && error.code < 600) {
        return Response.json({ error: error.message }, { status: error.code });
      }
      console.error("HTTP request failed", error);
      return Response.json({ error: "Internal server error" }, { status: 500 });
    }
  };

  const fetch: HttpHandler = async (request, context) => {
    const response = await dispatch(request, context);
    if (request.method !== "HEAD") return response;
    await response.body?.cancel().catch((error) => console.error("HTTP body cancellation failed", error));
    return new Response(null, response);
  };

  return { route, fetch };
}
