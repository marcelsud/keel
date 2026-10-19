# Keel

Build services in Go and TypeScript with typed clients, shared contracts, and independent upgrades.

Keel is an early prototype. Local contract generation, one-command development, cross-language calls, persistent data, events, background jobs, and concurrent API versions work today. GitHub contract installation and dated deprecation notices are planned.

The TypeScript SDK is packaged as [`@marcelsud/keel.ts`](sdks/typescript/README.md). See its README for package usage and publishing.

## Install the CLI

On Linux, macOS, or Windows with WSL:

```sh
curl -fsSL https://github.com/marcelsud/keel/releases/latest/download/install.sh | sh
```

The installer downloads the latest stable GitHub release for your machine, verifies its checksum, and installs `keel` in `~/.local/bin`. It updates your shell profile when needed. Open a new terminal, then run:

```sh
keel --version
```

The release includes the contract generators. You do not need to clone the repository, install Go, or install generators separately to use the CLI. Your services still need their own language runtime: Go 1.26+ for Go services, or Node.js 22+ for TypeScript services and generation.

Rerun the installer to upgrade. To install a specific version:

```sh
curl -fsSL https://github.com/marcelsud/keel/releases/download/keel-v0.2.0/install.sh | sh -s -- v0.2.0
```

<details>
<summary>Build the CLI from source</summary>

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

</details>

## Start a service

Use CLI v0.2.0 or later for `keel init` and development without protobuf. Rerun the installer above to upgrade. The [manual contract walkthrough](#start-with-a-protobuf-contract-optional) below also works with v0.1.0.

The TypeScript starter uses SDK 0.2.0's schema helpers.

With Bun 1.3+ installed:

```sh
keel init greeter --lang typescript
cd greeter
keel dev
```

`init` installs the SDK and creates a complete service, client, and startup configuration. The starter exposes a JSON `greet` action. `dev` starts the local environment and service, then watches for changes.

`service.ts` registers and runs the service. `client.ts` sends a request and exits. `keel.json` tells `keel dev` to run `service.ts`.

Actions declare their input shape directly, with inferred TypeScript types and automatic validation:

```ts
app.action({
  name: "greet",
  args: app.schema.object({ name: app.schema.string() }),
  handler(_ctx, input) {
    return { message: `Hello, ${input.name}!` };
  },
});
```

Wait for `greeter ready`. In a second terminal, from the same project directory:

```sh
bun run client.ts
```

Expected output:

```text
Hello, Ada!
```

Edit the greeting in `service.ts` and save. Keel restarts the service; call the client again to see the change. Ctrl-C stops both the service and the local environment.

Run one `keel dev` per project. A second terminal in that directory is for `bun run client.ts`; starting another environment against the same data directory reports that it is already running. For a separate project, choose a free port with `keel dev --port -1`.

The TypeScript project includes editor types and a compiler configuration. Run `bun run check` to type-check it; Bun runs the service without a separate build step.

For Go 1.26+:

```sh
keel init greeter --lang go
cd greeter
keel dev
```

In a second terminal, run `go run . -client` to receive the same greeting. Choose a different directory if you already created the TypeScript project.

`init` uses a new or empty directory and preserves existing projects. TypeScript is the default language. Use `--skip-install` to create the files now and install dependencies later.

Protobuf is optional. Add contracts under `proto/` when you need generated clients and shared API types across languages; `keel dev` starts generating bindings when those files appear. JSON actions validate their input, while the caller's result type annotation does not validate a remote response. The [SDK component guide](docs/sdk-guide.html) shows the available storage, events, jobs, and service APIs.

## Start with a protobuf contract (optional)

To start directly with a shared contract, this manual walkthrough starts in an empty directory and finishes with a typed client printing `Hello, Ada!`. It also works with the released CLI v0.1.0. Use Bun 1.3+ and Node.js 22+ for contract generation. Every file below is complete.

### 1. Install the SDK and prepare your project

Create your application outside the Keel checkout:

```sh
mkdir greeter
cd greeter
bun add @marcelsud/keel.ts
mkdir -p proto/greet/v1
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

Create `keel.json` to tell `keel dev` how to start your service:

```json
{
  "command": ["bun", "run", "service.ts"]
}
```

### 4. Write your service and client

Create `service.ts`:

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
for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.once(signal, async () => { await app.stop(); });
}
```

Create `client.ts`:

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

Keel detects TypeScript from the `package.json` created by Bun, generates the bindings under `generated/`, starts the local environment, and runs `bun run service.ts`. You do not need to run `keel` separately or run `keel generate` first.

Wait for `Greeter ready`. In a second terminal, from the same application directory:

```sh
bun run client.ts
```

Expected output:

```text
Hello, Ada!
```

Edit the greeting in `service.ts` and save. Keel regenerates and restarts the service; call the client again to see the change. Ctrl-C in the first terminal stops the service and local environment.

Generated contracts provide typed handlers and clients in your editor and TypeScript compiler. Bun runs TypeScript without type-checking; run your project's TypeScript checks in CI when using Bun.

### If setup fails

| Error | Fix |
| --- | --- |
| `add go.mod, package.json, or generation targets in keel.json` | For a proto-only directory, use `keel generate --lang typescript`. The walkthrough creates `package.json` before running Keel. |
| `set command in keel.json` | Add the `keel.json` from step 3. For a one-off run, use `keel dev --lang typescript -- bun run service.ts`. |
| Cannot find `@marcelsud/keel.ts` | Run `bun add @marcelsud/keel.ts` inside your application directory. |
| Cannot find a generated `.js` import | Run `keel dev` to generate the bindings. This walkthrough runs with Bun; Node.js needs the `.ts` files compiled first. |

After your first successful call, explore the [SDK component guide](docs/sdk-guide.html) for storage, actions, events, jobs, and versioned services. Its component snippets extend a connected application; they are not standalone programs.

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

## Develop before releasing

Put your contracts under `proto/` in your service project, then generate bindings locally:

```sh
keel generate
```

No release or running environment is required. Keel detects Go projects from `go.mod` and TypeScript projects from `package.json`, writing bindings under `generated/`. Use `--lang go` or `--lang typescript` when a project contains both. Go imports are inferred from your module.

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

This saves the local dependency in `keel.json` and generates its bindings in the consumer. For a contract at `notes/proto/notes/v2/notes.proto`, TypeScript imports it as:

```ts
import { NotesServiceContract } from "./generated/notes/v2/notes_keel.js";
```

The generated source is TypeScript; the `.js` import targets your compiled application. Go projects import the generated package under their own module.

Give your project a development command in `keel.json`:

```json
{
  "command": ["npm", "run", "start"]
}
```

For TypeScript, the `start` script should build and run your application, for example `tsc && node dist/server.js`. For Go, use `"command": ["go", "run", "."]`.

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

After creating and connecting `app` as in the walkthrough, the [TypeScript Notes example](examples/contracts/typescript.ts) registers handlers against its generated contract. The excerpts below use that example's `NotesV2` contract and are not standalone files. Requests and responses are checked as you write:

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

- [TypeScript service and client](examples/contracts/typescript.ts)
- [Go service and client](examples/contracts/go/main.go)
- [TypeScript events and background jobs](examples/notes.mjs)
- [Go events and background jobs](examples/notes/main.go)

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

## License

[MIT](LICENSE)
