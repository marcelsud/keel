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
curl -fsSL https://github.com/marcelsud/keel/releases/download/keel-v0.1.0/install.sh | sh -s -- v0.1.0
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

## Try it locally

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

Keel generates bindings, starts a local environment, and runs your command. It watches your source files and local contract dependencies, then regenerates and restarts on changes. Invalid contracts leave the previous service running until you fix them.

To share the first service's environment with another local project:

```sh
KEEL_URL=nats://127.0.0.1:4222 keel dev
```

Use `keel dev -- <command> [args]` to override the configured command, or `keel dev --watch-only` to regenerate without starting a service. Run `keel generate --help` for output and generator options.

## Write a handler, get a typed client

The [TypeScript example](examples/contracts/typescript.ts) registers handlers against a contract. Requests and responses are checked as you write:

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
