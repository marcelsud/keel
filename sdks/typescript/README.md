# @marcelsud/keel.ts

Use Keel from TypeScript to write services, call typed APIs, store data, publish events, and run background jobs.

## Install

Use Node.js 22+ or Bun 1.3+:

```sh
npm install @marcelsud/keel.ts
```

Or with Bun:

```sh
bun add @marcelsud/keel.ts
```

You need a running Keel environment. For the standalone storage example below, install the [Keel CLI](https://github.com/marcelsud/keel#install-the-cli) and run `keel` in another terminal first. To run a service with `keel dev`, configure its startup command in `keel.toml`, for example `command = ["bun", "run", "src/service.ts"]`. The [first-service walkthrough](https://github.com/marcelsud/keel#start-a-service) describes the new `keel init` flow and its CLI version requirements; protobuf is optional for JSON actions, storage, events, and jobs. Existing `keel.json` configurations remain supported; keep only one configuration file per project.

## Connect and store data

```ts
import { createKeel } from "@marcelsud/keel.ts";

const app = await createKeel({
  name: "notes",
  url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

try {
  const id = await app.collection.insert("notes", { body: "Hello" });
  console.log(await app.collection.get("notes", id));
} finally {
  await app.stop();
}
```

## Store and list documents

Use `insert()` for a generated ID or `create()` for a stable ID that must not overwrite an existing document:

```ts
await app.collection.create("movies", "orbit", {
  title: "Orbit", durationMs: 90 * 60_000,
});
const movies = await app.collection.list<{ title: string; durationMs: number }>("movies");
```

With matching core and SDK 0.2.1+, `create()` returns a 409 error if the ID exists. `list()` returns live documents in ID order, including each document's `id`; empty collections return `[]`. Listings are not a transaction across documents and are limited to 1000 documents / 512 KiB per response (413 if exceeded). `get()`, `update()`, and `delete()` operate on the same IDs.

For initial fixtures, pass `{ ifNeverExisted: true }` as the fourth argument to `create()`. This also returns 409 for deleted IDs, so a delayed seeder cannot restore a record someone removed. Ordinary `create()` allows explicitly recreating deleted IDs. Go supports the same behavior with `sdk.CreateOptions{IfNeverExisted: true}`.

## Update shared state safely

With the matching 0.2.1+ core and SDK, read the current revision before changing a value:

```ts
const inventory = app.kv.namespace("inventory");
const entry = await inventory.getEntry<{ remaining: number }>("screening-1");
const remaining = entry?.value.remaining ?? 100;
if (remaining > 0) {
  await inventory.compareAndSet("screening-1", { remaining: remaining - 1 }, entry?.revision ?? 0);
}
```

Revision zero creates a missing key. A conflicting write returns a 409 error: reread the value, check the business rule again, and retry. Keep external effects outside that retry. This protects one KV value; separate writes still commit independently. The [cinema example](https://github.com/marcelsud/keel/tree/main/examples/cinema) demonstrates concurrent seat holds and durable payment recovery.

## Query SQL and use transactions (local prototype)

SQL requires CLI v0.2.2+ and SDK v0.2.1+. Follow the [SQL setup](https://github.com/marcelsud/keel/blob/main/docs/development.md#use-sql-locally-prototype). Add a `[sql]` table with `notes = "migrations"` to `keel.toml`, create a `-- +goose Up` migration, and run `keel dev`. The CLI applies migrations before starting your command. Connect using the injected service identity:

```ts
const app = await createKeel({
  name: "notes",
  url: process.env.KEEL_URL,
  user: process.env.KEEL_USER,
  token: process.env.KEEL_TOKEN,
});

const id = await app.sql.transaction(async (tx) => {
  await tx.query("INSERT INTO notes (id, body) VALUES (?, ?)", ["note-1", "Hello"]);
  return "note-1";
});
const result = await app.sql.query<{ id: string; body: string }>(
  "SELECT id, body FROM notes WHERE id = ?", [id],
);
console.log(result.rows, result.rowCount);
await app.stop();
```

Handlers use the same API through `ctx.sql`. Go exposes `app.SQL.Query(ctx, statement, params...)` and `app.SQL.Transaction(ctx, func(tx *sdk.SQLTx) error { ... })`. Services send SQL through NATS and need no SQL driver or database credentials. Their Keel credentials allow their own database's queries and transactions; only the administrator can apply or inspect migrations.

`query()` accepts one parameterized statement and returns `{ rows, rowCount }`. Parameters are strings, finite numbers, booleans, or null, with at most 256 parameters. Use `?` placeholders. SQL TEXT stays a string, booleans bind as 1/0, SQL NULL returns null, blobs return `{ base64: string }`, and integers outside JavaScript's safe range return decimal strings. To send a large integer, pass a decimal string and `CAST(? AS INTEGER)`. Type parameters describe the expected rows; they do not validate the result or generate SQL types. Clustered dqlite SQL rejects NUL characters in TEXT parameters; use BLOB expressions for binary data. Prefer TEXT and INTEGER columns for consistent values across both engines; dqlite gives declared DATE, TIMESTAMP, and BOOLEAN columns special wire types.

For statements returning rows, `rowCount` is the number of returned rows; otherwise it is the direct affected-row count. Results are limited to 1000 rows / 512 KiB; oversized results fail and roll back their statement or transaction. Statements are limited to 64 KiB, values to 1 MiB, and runtime queries have a three-second deadline plus an instruction budget. Schema changes, migration metadata, session control, file access, and extension loading are reserved. Use migration files for DDL.

Await each transaction query and let the callback finish the transaction. Successful callbacks return their value after committing; thrown errors, query failures, and expired leases roll back. Leases last 30 seconds by default and remain on the core instance that began them. A restart invalidates old handles. Database leader failover can also abort an active transaction. A service that exits without finishing its transaction can hold the write lock until its lease expires. Standalone member commands accept `-sql-transaction-timeout 5s` to change this duration; the default is 30 seconds. The SDK does not retry transactions automatically. Catch `SQLCommitUnknownError` when commit delivery or its acknowledgement is uncertain, and reconcile with your idempotency key before retrying.

The standalone libSQL build has one connection per service and no replication. The [cluster build](https://github.com/marcelsud/keel/blob/main/docs/development.md#try-a-local-cluster-prototype) embeds dqlite and replicates SQL alongside replicated NATS storage. SQL requests from every core reach the database leader; SQL transactions use one service's database and do not include KV, collections, events, jobs, or another service's writes. Credential files and node hosts must be trusted. Per-service NATS credentials restrict network SQL requests; they do not sandbox local processes or protect the native engine's internal Unix sockets from local host users.

## Run separate cluster members (prototype)

Build with `sh scripts/build-cluster.sh` from the Keel checkout on Linux. Start three processes with distinct client ports, node names, route addresses, SQL addresses, and persistent data directories. All members share one private credentials file containing an administrator secret and distinct service secrets, each at least 24 characters:

```json
{
  "admin": "<administrator secret>",
  "services": { "notes": "<notes service secret>" }
}
```

For the first member, save `node-1.json`:

```json
{
  "name": "notes-cluster",
  "node": "node-1",
  "listen": "127.0.0.1:6222",
  "routes": ["nats-route://127.0.0.1:6223", "nats-route://127.0.0.1:6224"],
  "replicas": 3,
  "sqlListen": "127.0.0.1:9222",
  "sqlPeers": []
}
```

Create `node-2.json` and `node-3.json` with names `node-2` / `node-3`, route ports 6223 / 6224, SQL ports 9223 / 9224, routes to the other two members, and `"sqlPeers": ["127.0.0.1:9222"]`. Only the first member bootstraps with empty SQL peers. Start all three concurrently, one per terminal:

```sh
./keel -sql -credentials credentials.json -cluster-config node-1.json -port 4222 -data data/node-1
./keel -sql -credentials credentials.json -cluster-config node-2.json -port 4223 -data data/node-2
./keel -sql -credentials credentials.json -cluster-config node-3.json -port 4224 -data data/node-3
```

Keep credentials out of Git and restrict the file to its owner (`chmod 600 credentials.json`). For different hosts, replace loopback with stable addresses reachable by the members. Cluster routes and SQL replication use mutual TLS; the prototype's client NATS listener still needs a private trusted network. Keep the same cluster name, credentials, addresses, and data directories when restarting. Membership replacement, backups, and disaster recovery remain manual; do not bootstrap a replacement cluster over existing data.

From your service project, point migration commands and SDK clients at all members:

```sh
export KEEL_URL=nats://127.0.0.1:4222,nats://127.0.0.1:4223,nats://127.0.0.1:4224
/path/to/keel migrate up --credentials /path/to/credentials.json
```

Connect with `url: process.env.KEEL_URL`, `user: "notes"`, and that service's token. The TypeScript and Go SDKs accept comma-separated seed URLs. Migrations run on the SQL leader and serialize per service; check migration status after a failed request before retrying. Three replicas tolerate one failed member; five tolerate two. Loss of a majority stops writes rather than accepting divergent data.

## Validate an action

The schema helpers require SDK 0.2.0 or later. Before `app.start()`, declare an action on your connected app:

```ts
const createNote = app.action({
  name: "createNote",
  args: app.schema.object({
    body: app.schema.string(),
    labels: app.schema.string().array().optional(),
  }),
  async handler(ctx, input) {
    const id = await ctx.collection.insert("notes", input);
    return { id, body: input.body };
  },
});

// After app.start():
const note = await createNote({ body: "Hello" });
```

The SDK infers the handler input and action result. Missing or invalid fields return a 400 service error before the handler runs. Objects validate declared fields and strip extra fields; strings are checked without coercion or trimming.

Use `string()`, `number()`, `integer()`, `boolean()`, `object()`, `array()`, `record()`, `enum()`, `literal()`, `union()`, `null()`, and `unknown()`, with `.optional()` and `.nullable()` to compose shapes. You can also import `schema` and `type Infer` directly from the package, or continue supplying your own `args.parse()` parser. These helpers validate JSON actions; protobuf contracts remain the way to share generated API types across languages.

## Expose HTTP routes (0.2.1+)

SDK 0.2.1+ supports the Simplebase `app.route(method, path, definition)` API. The Node adapter runs on Node.js and Bun:

```ts
import { createKeel } from "@marcelsud/keel.ts";
import { node } from "@marcelsud/keel.ts/http/node";

const app = await createKeel({
  name: "notes", url: process.env.KEEL_URL, token: process.env.KEEL_TOKEN,
  http: node({ port: 3000 }),
});
const create = app.action({
  name: "create",
  args: app.schema.object({ title: app.schema.string() }),
  handler: (_ctx, input) => input,
});
app.route("GET", "/health", () => ({ ok: true }));
app.route("POST", "/notes", { action: create, status: 201 });
app.route("GET", "/notes/:id", ({ params, query }) => ({ id: params.id, query }));
await app.start();
console.log(app.http?.url.href);
```

Declare routes before `start()`. Definitions accept a handler or an action, `body`, `params`, and `query` parsers, an `input(context)` mapper for actions, an `output(result, context)` mapper, and `status`. Action input defaults to the JSON body, or `{}` when absent. Handlers receive `{ request, params, query, body, native }`; `body` is parsed when a body parser is supplied, otherwise use `request.json()`. Repeated query keys become arrays. JSON values become JSON responses; `undefined` returns 204. Return a `Response` to control headers, status, or streaming.

Routes support `:parameters`, literal path precedence, HEAD fallback, automatic OPTIONS, and 404/405 responses with `Allow`. Validation failures return 400; public NATS service errors retain their HTTP status. Unexpected errors return a generic 500. Output mapping happens after an action completes and cannot undo its effects.

Configure the adapter in either `createKeel({ http })` or `start({ http })`. `app.fetch(request)` supports an external Fetch host without an adapter. `stop()` rejects new HTTP requests with 503 and drains admitted requests while NATS actions remain available. Controllers, decorators, and framework-specific adapters are outside this API.

Go provides the equivalent `app.Route(method, path, handlerOrRoute)` and implements `http.Handler`. Configure `sdk.Options{HTTP: &sdk.HTTPOptions{Address: "127.0.0.1:3000"}}` to let `Start` own a listener; its URL is `app.HTTP.URL`. An `sdk.HTTPRoute{Action: "create", Status: 201}` calls that app's registered JSON action through NATS. Direct handlers have type `func(*sdk.HTTPContext) (any, error)`. Route `Body` parsers can transform JSON; `Params` and `Query` callbacks validate their respective maps. `Input` and `Output` map action input and responses. Return `*http.Response` for raw responses or `*sdk.Error` for a public HTTP error. A nil handler result defaults to 204.

## Start and stop a service

After registering your handlers, start the service:

```ts
await app.start();
```

SDK 0.2.1+ handles SIGINT and SIGTERM automatically, draining active work and closing the connection. To manage shutdown yourself, use `await app.start({ signals: false })` and call `await app.stop()` when ready. Repeated calls to `start()` use the first call's options. Calling `stop()` or failing to start removes the SDK's signal listeners; other listeners are preserved. Clients that never call `start()` still close their connection with `stop()`.

## Call a typed service

Install the [Keel CLI](https://github.com/marcelsud/keel#install-the-cli). Its release bundle includes the generators; TypeScript generation uses your Node.js runtime.

If you own the service, put its contracts under `proto/` and run:

```sh
keel generate
```

If you consume a service from a neighboring checkout, run in your consumer project:

```sh
keel use ../notes
```

Both commands generate into `generated/` without a release or running service. `use` also saves the local dependency in `keel.toml`, or updates an existing `keel.json`. Run `keel generate` again after changes, or use [keel dev](https://github.com/marcelsud/keel/blob/main/docs/development.md#develop-before-releasing) to regenerate and restart your service automatically.

Import the generated contract to get completion and type checking for its methods, requests, and responses. For `proto/notes/v2/notes.proto`, the generated source is `generated/notes/v2/notes_keel.ts`; use a `.js` import for your compiled application:

```ts
import { createKeel } from "@marcelsud/keel.ts";
import { NotesServiceContract } from "./generated/notes/v2/notes_keel.js";

const app = await createKeel({
  name: "notes-client",
  url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

try {
  const notes = app.client(NotesServiceContract);
  const note = await notes.createNote({
    content: { text: "Hello from TypeScript" },
  });
  console.log(note.id);
} finally {
  await app.stop();
}
```

Contracts belong to your project. See the [complete service example](https://github.com/marcelsud/keel/blob/main/examples/contracts/typescript.ts) for service registration, validation, and version migration.

## Background retries

Core 0.2.1+ retries failed subscriptions and background jobs until acknowledged, including after a service restart. Handlers must tolerate duplicate deliveries. The prototype has no automatic dead-letter queue; inspect and fix permanently failing handlers or payloads.

## Publish this SDK

From a checkout of the Keel repository, install the development dependencies using the repository's setup instructions. Publishing also requires Go 1.26+ and Node.js 24+ to run the cross-language examples in the tests.

Run these commands from `sdks/typescript`:

```sh
npm login
npm publish --dry-run
npm publish
```

Publishing runs the tests and builds the package. Packing with `npm pack` also builds it. Consumers receive JavaScript, TypeScript declarations, this README, and the MIT license.

For GitHub Actions, configure an [npm trusted publisher](https://docs.npmjs.com/trusted-publishers/) for this package:

- GitHub owner: `marcelsud`
- Repository: `keel`
- Workflow filename: `publish-npm.yml`
- Allow direct publishing with `npm publish`.

For a new package, make the first publish manually, then configure its trusted publisher. The workflow uses OIDC authentication.

Set the SDK version in `package.json`, then push a matching tag such as `keel.ts-v0.1.0`. You can also run **Publish TypeScript SDK** manually from GitHub Actions. Stable versions publish to `latest`; prerelease versions publish to `next` in the workflow. For a manual prerelease, use `npm publish --tag next`.

## License

[MIT](https://github.com/marcelsud/keel/blob/main/LICENSE)
