# Development guide

Start with the [README](../README.md) to create and run your first service. This guide covers contracts, local development, SQL, clustering, and contributing from a checkout. Run repository commands from the Keel repository root unless a section says otherwise.

## Build the CLI from source

For contributors with Go 1.26+, install from the checkout and add Go's binaries to your PATH:

```sh
go install -buildvcs=false ./cmd/keel
keel_bin="$(go env GOBIN)"
export PATH="${keel_bin:-$(go env GOPATH)/bin}:$PATH"
go install github.com/bufbuild/buf/cmd/buf@v1.65.0
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
```

Source builds use local generators. The checkout installs its TypeScript generator with the SDK's development dependencies; other TypeScript projects can install it with:

```sh
npm install --save-dev @bufbuild/protoc-gen-es@2.16.0
```

Persist the PATH line in your shell profile when building from source.

## Start with a protobuf contract (optional)

To start directly with a shared contract, this manual walkthrough starts in an empty directory and finishes with a typed client printing `Hello, Ada!`. TOML configuration requires the CLI from this checkout; existing `keel.json` configurations remain supported. Keep only one configuration file per project. Use Bun 1.3+ and Node.js 22+ for contract generation. Every file below is complete.

### 1. Install the SDK and prepare your project

Create your application outside the Keel checkout:

```sh
mkdir greeter
cd greeter
bun add @marcelsud/keel.ts
mkdir -p proto/greet/v1 src
```

This creates `package.json` and installs the SDK. The CLI installer includes the contract generators; installing the SDK is a separate step.

### 2. Define your contract

Create `proto/greet/v1/greet.proto`:

```proto
syntax = "proto3";
package greet.v1;

service GreeterService {
  rpc Greet(GreetRequest) returns (GreetResponse);
}

message GreetRequest { string name = 1; }
message GreetResponse { string message = 1; }
```

### 3. Configure startup

Create `keel.toml` to tell `keel dev` how to start your service:

```toml
command = ["bun", "run", "src/service.ts"]

[[targets]]
language = "typescript"
out = "src/generated"
```

### 4. Write your service and client

Create `src/service.ts`:

```ts
import { createKeel, ValidationError } from "@marcelsud/keel.ts";
import { GreeterServiceContract } from "./generated/greet/v1/greet_keel.js";

const app = await createKeel({
  name: "greeter",
  url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

app.service(GreeterServiceContract, {
  async greet(_ctx, input) {
    if (!input.name.trim()) throw new ValidationError("name is required");
    return { message: `Hello, ${input.name}!` };
  },
});
await app.start();
console.log("Greeter ready");
```

With SDK 0.2.1+, `app.start()` handles SIGINT and SIGTERM automatically and waits for active work to finish. Use `app.start({ signals: false })` to manage shutdown yourself, and call `app.stop()` when ready.

Create `src/client.ts`:

```ts
import { createKeel } from "@marcelsud/keel.ts";
import { GreeterServiceContract } from "./generated/greet/v1/greet_keel.js";

const app = await createKeel({
  name: "greeter-client",
  url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

try {
  const result = await app.client(GreeterServiceContract).greet({ name: "Ada" });
  console.log(result.message);
} finally {
  await app.stop();
}
```

The generated **service contract** goes into `app.service()` and `app.client()`. The protobuf RPC `Greet` becomes the TypeScript method `greet`; request messages such as `GreetRequest` are not service contracts. Generation writes `.ts` files. Bun resolves the `.js` import above to the generated TypeScript; compiled Node.js applications use that same import for their JavaScript output.

### 5. Start it and make a request

In your application directory:

```sh
keel dev
```

Keel detects TypeScript from the `package.json` created by Bun, generates the bindings under `src/generated/`, starts the local environment, and runs `bun run src/service.ts`. You do not need to run `keel` separately or run `keel generate` first.

Wait for `Greeter ready`. In a second terminal, from the same application directory:

```sh
bun run src/client.ts
```

Expected output:

```text
Hello, Ada!
```

Edit the greeting in `src/service.ts` and save. Keel regenerates and restarts the service; call the client again to see the change. Ctrl-C in the first terminal stops the service and local environment.

Generated contracts provide typed handlers and clients in your editor and TypeScript compiler. Bun runs TypeScript without type-checking; run your project's TypeScript checks in CI when using Bun.

### If setup fails

| Error | Fix |
| --- | --- |
| `cannot detect generation language` | For a proto-only directory, use `keel generate --lang typescript`. The walkthrough creates `package.json` and configures the TypeScript target before running Keel. |
| `set command in keel.toml or keel.json` | Add the `keel.toml` from step 3. For a one-off run, use `keel dev --lang typescript -- bun run src/service.ts`. |
| Cannot find `@marcelsud/keel.ts` | Run `bun add @marcelsud/keel.ts` inside your application directory. |
| Cannot find a generated `.js` import | Run `keel dev` to generate the bindings. This walkthrough runs with Bun; Node.js needs the `.ts` files compiled first. |

After your first successful call, explore the [SDK component guide](sdk-guide.html) for storage, actions, events, jobs, and versioned services. Its component snippets extend a connected application; they are not standalone programs.

## Run the repository examples

To run the repository examples from a checkout, install the CLI above and use Go 1.26+, Node.js 24+, and Bun 1.3+. Run from the repository root:

```sh
bun install --cwd sdks/typescript --frozen-lockfile
bun run --cwd sdks/typescript build
```

Start the environment and the Go Notes service together:

```sh
keel dev
```

Call it from another terminal:

```sh
node examples/contracts/typescript.ts --client
```

The client prints results for both API versions. Editing a local contract regenerates its bindings and restarts the service. Editing service source also restarts it. Ctrl-C stops the service and environment.

To try a TypeScript service instead, stop `keel dev` and run:

```sh
keel dev -- npm --prefix sdks/typescript run example:service
```

Then call it from Go in another terminal:

```sh
go run ./examples/contracts/go -client
```

## Use SQL locally (prototype)

The checkout includes opt-in embedded SQL. Use the matching local SDK and build a local CLI; released CLI binaries do not include this prototype. On Linux (amd64/arm64) or Apple Silicon macOS with Go 1.26+ and a C compiler:

```sh
CGO_ENABLED=1 go build -tags keel_libsql -o .keel/bin/keel ./cmd/keel
```

In your service project, add a migration directory to `keel.toml`:

```toml
command = ["bun", "run", "src/service.ts"]

[sql]
notes = "migrations"
```

Use the CLI you just built for these commands:

```sh
/path/to/keel/.keel/bin/keel migrate create create_notes
```

Edit the new migration:

```sql
-- +goose Up
CREATE TABLE notes (id TEXT PRIMARY KEY, body TEXT NOT NULL);
```

Connect with `name: "notes"`, `url: process.env.KEEL_URL`, `user: process.env.KEEL_USER`, and `token: process.env.KEEL_TOKEN`, then use SQL from the app or a handler:

```ts
await app.sql.transaction(async (tx) => {
  await tx.query("INSERT INTO notes (id, body) VALUES (?, ?)", ["note-1", "Hello"]);
});
const { rows } = await app.sql.query("SELECT * FROM notes WHERE id = ?", ["note-1"]);
```

Run `/path/to/keel/.keel/bin/keel dev`. It creates local credentials, starts the environment, applies migrations, and passes the service's credentials to its command. Changes to `.sql` files restart the command after applying new migrations. Empty new migrations remain drafts until you add SQL. Create a new migration for each schema change; editing an already applied file does not rerun it. `keel migrate status` lists applied and pending migrations; `keel migrate up` applies pending migrations to a running environment. Set `KEEL_URL` when using a nondefault port. Use `--service notes` when a project has multiple SQL services.

For multiple services, list their names in `services` and map each SQL owner to its migrations in `sql`. The launcher distributes each process's own credential; the [cinema launcher](../examples/cinema/tools/dev.ts) shows this. Keep `.keel/credentials.json` private and out of version control. Runtime credentials can query only their own SQL database; migration commands use the administrator credential. Processes sharing your local OS account can still read that account's files.

Transactions commit on callback success and roll back on errors, query failures, or expiry (30 seconds by default). Await every query. A lost commit response has an unknown outcome: reconcile by an operation's idempotency key before retrying. SQL transactions cover one service's database; events, jobs, KV writes, and other services commit independently. The build above runs one standalone core. See the [SDK SQL API](../sdks/typescript/README.md#query-sql-and-use-transactions-local-prototype) for value types and limits.

## Try a local cluster (prototype)

On Linux, including WSL2, build the clustered CLI from this checkout. Install Go 1.26+, a C compiler, and the native build tools first. On Ubuntu/Debian:

```sh
sudo apt install build-essential pkg-config autoconf automake libtool curl libsqlite3-dev libuv1-dev liblz4-dev
sh scripts/build-cluster.sh
```

The script downloads a pinned database release and builds a static executable at `.keel/bin/keel`. Released CLI binaries do not yet include clustered SQL. From your SQL service project, use that executable:

```sh
/path/to/keel/.keel/bin/keel dev --nodes 3
```

Your service code, SQL configuration, and migration files stay the same. Keel starts three local members, applies migrations, and passes their connection URLs and your service's credentials to the command. Restarting preserves the cluster's addresses and data under `.keel/`. Standalone and clustered SQL use separate data directories; switching modes does not import existing data.

A three-member cluster can keep working after one member fails. Without a majority, writes fail. Transactions in progress can fail during a core restart or database failover; reconcile uncertain commits before retrying. Keep backups: replication does not protect against accidental deletion or permanent loss of a majority.

`dev --nodes 3` runs all three members inside the development CLI. To test separate processes or hosts, use the [cluster member configuration](../sdks/typescript/README.md#run-separate-cluster-members-prototype). Each deployed member runs one executable with its own data directory. This prototype assumes trusted hosts and does not provide an OS process sandbox.

## Run the cinema application

The [cinema example](../examples/cinema) runs five microservices from this checkout, so you can try SDK changes before publishing them:

| Service | Language | Interface |
| --- | --- | --- |
| Catalog | TypeScript | JSON actions |
| Booking | Go | Generated protobuf RPC over NATS |
| Payments | TypeScript | Generated protobuf RPC over NATS |
| Checkout | Go | JSON actions |
| Notifications | TypeScript | JSON events and background jobs |

Open `services/<name>/` to work on a service; its migrations and fixtures live beside its source. Shared code is in `packages/shared-go/` and `packages/shared-ts/`, protobuf contracts and generated clients are in `contracts/`, development tooling is in `tools/`, integration tests are in `tests/`, and the demo is in `client/`. Run the commands below from `examples/cinema`.

For TypeScript, start at `main.ts` and the service's `*.module.ts`: controllers expose operations, providers implement them, and repositories access storage. Go services start at `cmd/main.go`, with each capability under `internal/` owning its handlers and use cases.

With Go 1.26+, a C compiler, Node.js 24+, Bun 1.3+, and the Keel CLI installed, run on Linux (amd64/arm64) or Apple Silicon macOS:

```sh
cd examples/cinema
bun run setup
bun run dev
```

Setup uses the local SDK, generates the contracts, and builds the services and a local CLI with SQL enabled. `dev` starts one environment, applies each service's migrations, and starts all five services with separate credentials; saving a service, migration, or contract restarts the application. Ctrl-C stops everything.

To use a three-member cluster on Linux/WSL2, install the [cluster build prerequisites](#try-a-local-cluster-prototype), then replace the setup and development commands with:

```sh
bun run setup:cluster
bun run dev:cluster
```

On first start, Catalog loads its movies, cinemas, auditoriums, and seat maps from [fixtures](../examples/cinema/services/catalog/fixtures.ts) into SQL in one transaction. Later starts preserve edits and deletions. Application operations read their service's SQL tables; screenings, orders, reserved seats, tickets, payment attempts, payments, outboxes, and delivery receipts persist across restarts. Migrations upgrade records from the earlier SQL prototype. Existing KV/collection data from earlier versions of this example is not imported. Edit the fixtures before starting a fresh development environment to change the initial data.

In another terminal, from `examples/cinema`:

```sh
bun run demo
bun run demo --cancel
```

The demo schedules a screening, holds two seats, pays through Checkout, prints the tickets, and waits for a receipt. `--cancel` also cancels the purchase and waits for its full refund. Each run creates a new screening.

The application supports multiple cinemas and auditoriums, blocked seats, expiring holds, fixed quotes, payment retries, cancellation cutoffs, and operator screening cancellation. The payment simulator exercises approval, rejection, delayed results, missing callbacks, duplicate callbacks, and temporary refund failures. Notifications retry independently of the purchase.

Run its checks independently; the tests start their own isolated environment:

```sh
bun run check
bun run test
```

For clustered tests, run `KEEL_SQL_ENGINE=dqlite bun run test`. These tests start three separate core processes and include an abrupt core failure.

The tests cover competing Booking replicas, all-or-nothing seat claims, late-payment refunds, idempotent requests, cancellation races, notification retries, and service restarts. Payments and receipts are simulated; customer IDs assume a trusted service network. This example caps screenings at 100, orders per screening at 100, payment attempts per order at five, and provider attempts at 500. Use a fresh development data directory when you want to start over.

## Develop before releasing

Put your contracts under `proto/` in your service project, then generate bindings locally:

```sh
keel generate
```

No release or running environment is required. Keel detects Go projects from `go.mod` and TypeScript projects from `package.json`, writing bindings under `generated/` by default. TypeScript starters configure `src/generated/` in `keel.toml`. Use `--lang go` or `--lang typescript` when a project contains both. Go imports are inferred from your module.

For a directory containing only protobuf files, choose the language explicitly:

```sh
keel generate typescript
```

This shorthand requires CLI v0.2.0 or later; v0.1.0 uses `keel generate --lang typescript`. TypeScript generation does not require `package.json`. For Go, use `keel generate go` in a module with `go.mod`, or provide `--go-package-prefix` explicitly.

In a consumer project next to your service checkout:

```sh
cd ../orders
keel use ../notes
```

This saves the local dependency in `keel.toml` (or updates an existing `keel.json`) and generates its bindings in the consumer. For a contract at `notes/proto/notes/v2/notes.proto`, a TypeScript starter imports it from `src/service.ts` as:

```ts
import { NotesServiceContract } from "./generated/notes/v2/notes_keel.js";
```

The generated source is TypeScript; the `.js` import targets your compiled application. Go projects import the generated package under their own module.

Give your project a development command in `keel.toml`:

```toml
command = ["npm", "run", "start"]
```

For TypeScript, the `start` script should build and run your application, for example `tsc && node dist/server.js`. For Go, use `command = ["go", "run", "."]`.

```sh
keel dev
```

Keel generates any contracts, starts a local environment, and runs your command. It watches your source files and local contract dependencies, then regenerates and restarts on changes. Development can start with no contracts; invalid contracts leave the previous service running until you fix them.

To share the first service's environment with another local project:

```sh
KEEL_URL=nats://127.0.0.1:4222 keel dev
```

Use `keel dev -- <command> [args]` to override the configured command, or `keel dev --watch-only` to regenerate without starting a service. Run `keel generate --help` for output and generator options.

## Write a handler, get a typed client

After creating and connecting `app` as in the walkthrough, the [TypeScript Notes example](../examples/contracts/typescript.ts) registers handlers against its generated contract. The excerpts below use that example's `NotesV2` contract and are not standalone files. Requests and responses are checked as you write:

```ts
app.service(NotesV2, {
  async createNote(ctx, input) {
    if (!input.content?.text.trim()) {
      throw new ValidationError("content.text must not be empty");
    }

    const id = await ctx.collection.insert("notes", {
      text: input.content.text,
    });

    return { id, content: input.content };
  },
});
```

Call the service through the same contract:

```ts
const notes = app.client(NotesV2);

const note = await notes.createNote({
  content: { text: "Hello from another service" },
});

console.log(note.id);
```

Open the examples for complete imports and startup code:

- [TypeScript service and client](../examples/contracts/typescript.ts)
- [Go service and client](../examples/contracts/go/main.go)
- [TypeScript events and background jobs](../examples/notes.mjs)
- [Go events and background jobs](../examples/notes/main.go)

## Upgrade clients when they are ready

Both Notes examples serve v1 and v2 together. Existing clients can keep using v1 while other clients adopt v2. Keep the old handler working during that migration.

To try retiring v1, stop the Notes service and restart it with:

```sh
KEEL_RETIRE_V1=1 keel dev
```

A v1 request now receives a `410` service error with its replacement. The v2 API remains available. The example clients call v1 first, so they exit at that error when v1 is retired.

## Planned developer workflow

The following commands describe the intended experience. They are not implemented yet.

```sh
keel use github:acme/notes@v1.4.0
keel update github:acme/notes@v1.5.0
keel check
```

- `use github:...` will install a contract from a service's GitHub release and generate typed bindings for your project.
- `update` shows contract changes and checks compatibility before updating the pinned dependency.
- `check` validates contracts and generated bindings before you publish.

Service owners will publish contracts alongside their application releases. Consumers will pin an exact release, build from local artifacts, and choose when to update. Publishing a release will not update a consumer or deploy a server automatically.

Lifecycle notices are also planned:

| State | What consumers can expect |
| --- | --- |
| `alpha` | Early preview with limited support. |
| `beta` | Available for integration testing and still evolving. |
| `stable` | Supported API with backward compatibility. This is the default when no state is declared. |
| `deprecated` | Continues working while announcing a replacement and EOL date. |
| `retired` | Returns a `410` service error with migration information. |

Deprecation notices will reach clients using older pinned contracts. The SDKs will surface the deadline without changing their API version automatically. Breaking changes will use a new contract version.

## Check your changes

After the initial setup:

```sh
KEEL_TYPESCRIPT_TEST=1 go test ./...
bun run --cwd sdks/typescript test
```

These checks cover Go and TypeScript clients and services, including calls across languages.
Local generation tests also compile fresh Go and TypeScript bindings and exercise development restarts. Install both contract generators before running them.
