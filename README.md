# Keel

Build software that grows with your business, without growing your infrastructure complexity.

Keel brings service communication, persistent storage, events, and background jobs into one development experience. Write your services in Go or TypeScript, start locally with one command, and run on your own infrastructure.

**Self-hosted by default.** Start with one node and explore clustering when you need resilience. Keel is an early prototype; clustered operation currently requires a build from this checkout.

## Start building

### Install the CLI

On Linux, macOS, or Windows with WSL:

```sh
curl -fsSL https://github.com/marcelsud/keel/releases/latest/download/install.sh | sh
```

The installer downloads a release, verifies its checksum, and installs `keel` in `~/.local/bin`. It updates your shell profile when needed. Open a new terminal, then check:

```sh
keel --version
```

With CLI v0.2.1 or later, use `keel upgrade` to update the CLI and bundled generators. For older CLIs, rerun the installer. See the [CLI reference](docs/cli.html#upgrade) for version selection and custom installations.

### Start a service

With Bun 1.3+ installed, create your project outside the Keel checkout:

```sh
mkdir greeter
cd greeter
keel init
```

Keel installs the dependencies and creates a service, a client, and the configuration to run them. `keel dev` starts your local environment and service, then watches for changes.

With CLI v0.2.1 or later, `keel init` asks for the directory, language, and starter when run in a terminal. Choose the JSON example for this walkthrough. You can also start empty or use a Protobuf greeting example; use `--yes` to accept defaults in scripts. See the [setup options](docs/cli.html#init).

Start your service:

```sh
keel dev
```

Wait for `greeter ready`. In a second terminal, from the same directory:

```sh
bun run src/client.ts
```

You should see:

```text
Hello, Ada!
```

Edit the greeting in `src/service.ts` and save. Keel restarts the service; run the client again to see your change. Ctrl-C stops the service and its local environment. Run one `keel dev` per project; use the second terminal for the client.

Your project contains:

| File | Purpose |
| --- | --- |
| `src/service.ts` | Define what your service does. |
| `src/client.ts` | Call your service from another application. |
| `src/generated/` | Generated TypeScript bindings when using Protobuf. |
| `keel.toml` | Configure how your project starts. |

New projects use `keel.toml`. Existing `keel.json` files remain supported; keep only one configuration file per project. `keel use` updates whichever format the project already uses.

Run `bun run check` to check your TypeScript types.

<details>
<summary>Prefer Go?</summary>

With Go 1.26+ installed, create a separate project:

```sh
keel init greeter-go --lang go
cd greeter-go
keel dev
```

In another terminal, from that directory:

```sh
go run . -client
```

</details>

## Write your first service

The generated `src/service.ts` already connects and starts your application. This is where you define its greeting:

```ts
app.action({
  name: "greet",
  args: app.schema.object({ name: app.schema.string() }),
  handler(_ctx, input) {
    return { message: `Hello, ${input.name}!` };
  },
});
```

The generated `src/client.ts` connects separately and calls it:

```ts
const result = await app.call<{ message: string }>(
  "greeter",
  "greet",
  { name: "Ada" },
);
console.log(result.message);
```

These snippets are excerpts from the generated files. Keep their connection and startup code as you add your own operations.

## Build a complete application

Use the same SDK as your application grows:

| When you need to… | Use |
| --- | --- |
| Share work between services | Service calls with input validation and structured errors |
| Expose an HTTP API | Routes backed by handlers or NATS actions — SDKs v0.2.1+ |
| Keep application data | Persistent collections and key-value storage |
| Notify other parts of your application | Events and subscriptions |
| Run work outside a request | Background jobs with retries |
| Update related records together | SQL queries, transactions, and migrations — checkout prototype |

The [SDK guide](docs/sdk-guide.html) shows how to use each component. SQL transactions cover one service's database; separate storage operations and other services commit independently.

## Grow at your pace

Add services as your application needs them. Go and TypeScript services can work together, and versioned APIs let clients upgrade at their own pace.

Keep development on your machine and deployment on infrastructure you control. The clustering prototype lets multiple nodes preserve committed data and continue operating after a member fails, provided a majority remains available. See the [development guide](docs/development.md#try-a-local-cluster-prototype) when you are ready to try it.

## See a complete application

The [cinema application](examples/cinema) demonstrates five Go and TypeScript services working together to sell assigned seats across multiple cinemas.

It covers expiring reservations, competing buyers, payment retries, tickets, cancellation and refunds, and receipt delivery. Business records persist across restarts. Payments are simulated, including delays, failures, and duplicate results.

Follow the [cinema setup](docs/development.md#run-the-cinema-application) to run a purchase, cancel it, and explore recovery and clustering.

## Project status

Keel is an early prototype.

| Availability | Features |
| --- | --- |
| Released | Interactive project creation, TOML configuration, CLI upgrades, local development, HTTP routes, Go and TypeScript services, API versions, collections, key-value storage, events, and jobs |
| Available from this checkout | SQL and migrations, per-service database credentials, clustering, and the cinema application |
| Planned | Installing shared contracts from GitHub releases, compatibility checks, and advance deprecation notices |

The install command above uses released binaries. SQL and clustering require the local builds described in the development guide.

## Documentation

- [SDK guide](docs/sdk-guide.html) — practical examples for each component.
- [CLI reference](docs/cli.html) — installation, upgrades, and available commands.
- [TypeScript SDK](sdks/typescript/README.md) — API usage and package details.
- [Development guide](docs/development.md) — shared contracts, migrations, cluster setup, repository examples, and contributor checks.
- [Go service and client](examples/contracts/go/main.go) — a complete Go example.

## License

[MIT](LICENSE)
