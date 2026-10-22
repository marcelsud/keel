# Keel CLI reference

[SDK guide](sdk.md) · [CLI reference](cli.md) · [Development guide](development.md)

> Keel is an early prototype.

`keel init` creates a service project. `keel dev` runs it with a local Keel environment and restarts it after source changes. Other commands generate or fetch contracts, upgrade the CLI, and manage SQL migrations.

## Contents

- [Installing Keel CLI](#installing-keel-cli)
- [Updating Keel CLI](#updating-keel-cli)
- [Checking the version](#checking-the-version)
- [keel init](#keel-init)
- [keel dev](#keel-dev)
- [keel generate](#keel-generate)
- [keel use](#keel-use)
- [keel pull](#keel-pull)
- [keel migrate](#keel-migrate)
- [Starting an environment](#starting-an-environment)
- [Getting help](#getting-help)

## Installing Keel CLI

Install the CLI on Linux amd64/arm64, Apple Silicon macOS, or Windows with WSL.

Install the latest stable release:

```sh
curl -fsSL https://github.com/marcelsud/keel/releases/latest/download/install.sh | sh
```

Check the installation:

```sh
keel --version
```

CLI v0.2.2 and later include embedded libSQL. Intel Mac release bundles are unavailable.

The installer verifies the release checksum and installs the CLI with its contract generators. It uses `~/.local/bin` and `~/.local/share/keel` by default. It adds the binary directory to your PATH in your shell profile when needed. Open a new terminal after installation.

Set `KEEL_BIN_DIR` and `KEEL_INSTALL_DIR` to choose other absolute paths. Set `KEEL_NO_MODIFY_PATH=1` to leave shell profiles unchanged.

## Updating Keel CLI

`keel upgrade` installs the latest stable CLI release and its bundled generators. It requires CLI v0.2.1 or later. For an older CLI, rerun the installer.

Upgrade to the latest stable release:

```sh
keel upgrade
```

Choose a published release:

```sh
keel upgrade v0.2.1
```

Check the active version:

```sh
keel --version
```

Upgrades detect the active installation, including custom directories. They verify the installer and archive checksums and leave shell profiles unchanged. A failed download or checksum check preserves the active release.

The command updates the CLI bundle. Update your application's SDK dependency separately. Rebuild source and clustered SQL installations from their checkout.

## Checking the version

`keel --version` and `keel version` print the version of the executable on your PATH.

```sh
keel --version
keel version
```

A source build reports `dev` unless the build sets a release version. The CLI and TypeScript SDK have independent releases.

## keel init

`keel init` creates a service project and installs its dependencies. In an interactive terminal, it asks for the directory, language, and starter.

Start interactive setup:

```sh
keel init
```

Set the TypeScript service command in `keel.toml`:

```toml
command = ["bun", "run", "src/service.ts"]

[[targets]]
language = "typescript"
out = "src/generated"
```

Create a Go Protobuf project without prompts:

```sh
keel init greeter-go --lang go --template protobuf --yes
cd greeter-go
keel dev
```

Create an empty TypeScript service:

```sh
keel init my-service --template empty --yes
```

Create the files without installing dependencies:

```sh
keel init greeter --skip-install --yes
```

Interactive prompts require CLI v0.2.1 or later. Keel asks for choices you did not supply. The defaults are the current directory, TypeScript, and the JSON starter. Choose a new or empty directory.

TypeScript requires Bun 1.3+. Go requires Go 1.26+. TypeScript Protobuf generation also requires Node.js 22+.

New projects use `keel.toml`. Keel accepts existing `keel.json` files, but reports an ambiguity error if both files exist. TypeScript source files live in `src/`, with generated bindings in `src/generated/`. The empty starter has no sample actions or client. The Protobuf starter includes a greeting contract, generated bindings, and a typed client.

Use `--yes` or `-y` to accept defaults without prompts. Redirected stdin also uses defaults. `--skip-install` creates files without installing dependencies. Keel prints the remaining steps, including generation for Protobuf projects.

## keel dev

`keel dev` starts the local environment and service. It regenerates contracts and restarts the service when source files change.

Start the configured service:

```sh
keel dev
```

Choose another port:

```sh
keel dev --port 4223
```

Connect to an existing environment:

```sh
KEEL_URL=nats://127.0.0.1:4222 keel dev
```

Pass the service command after `--`:

```sh
keel dev -- bun run src/service.ts
```

Keel reads the service command from `keel.toml` or `keel.json`. You can also pass a command after `--`. A JSON service starts without Protobuf contracts. The message about skipping generation is informational.

Set `KEEL_URL` to reuse a running environment. `--watch-only` regenerates contracts without starting a service or environment. Run one dev process per project. Ctrl-C stops the processes it owns.

CLI v0.2.2+ includes standalone SQL. The `--nodes 3` option requires the native cluster build from the checkout.

## keel generate

`keel generate` creates Go or TypeScript bindings from Protobuf contracts in the project and its local dependencies.

Generate bindings with the project configuration:

```sh
keel generate
```

Choose the language and output directory:

```sh
keel generate typescript --out src/generated
keel generate go --out generated
```

Generation reads `proto/` and the targets and dependencies in `keel.toml` or `keel.json`. The project must contain contracts or reference them through local dependencies. During `keel dev`, a JSON-only project skips generation and still starts.

The CLI bundle includes Buf, `protoc-gen-go`, and `protoc-gen-es`. TypeScript generation also requires Node.js 22+. Keel infers the language and Go import prefix from the project. Use `--lang` to override a project with multiple targets.

## keel use

`keel use` adds contracts from a neighboring project and generates a client. The source service does not need to be running.

Add local contracts:

```sh
keel use ../notes
```

Choose a dependency alias:

```sh
keel use ../notes --as notes-api
```

Keel saves the dependency as a relative path in the project's `keel.toml` or `keel.json`. If neither file exists, it creates `keel.toml`. The default alias is the source directory name. Use `--as` to choose another alias.

GitHub release sources are planned. Run `keel generate` after a dependency changes, or let `keel dev` regenerate its bindings.

## keel pull

`keel pull` fetches a running service's complete Protobuf contract and generates a client.

Generate a TypeScript client:

```sh
keel pull notes.v2.NotesService --lang typescript --out src/generated
```

Generate a Go client:

```sh
keel pull notes.v2.NotesService --lang go --out generated --go-package-prefix example.com/app/generated
```

Accept a changed contract:

```sh
keel pull notes.v2.NotesService --lang typescript --out src/generated --update
```

Pass the fully qualified Protobuf service name. `KEEL_URL` and `KEEL_TOKEN` select the running environment and authenticate the request. `--url` overrides the URL.

A lock file pins the contract hash. Use `--update` to accept a changed contract. Deprecated and retired contracts remain discoverable. For Go output, set `--go-package-prefix` to match the generated code's import directory.

## keel migrate

`keel migrate` creates SQL migration files, applies them, or reports their state.

Set the migration directory in `keel.toml`:

```toml
[sql]
notes = "migrations"
```

Create a migration, apply it, and check its state:

```sh
keel migrate create initial --service notes
keel migrate up --credentials credentials.json
keel migrate status --credentials credentials.json
```

CLI v0.2.2+ includes standalone SQL. Applications that call SQL APIs need SDK v0.2.1 or later. Configure the service's migration directory in `keel.toml` or `keel.json` before running these commands.

Creating a migration only writes a local file. Applying migrations and checking their state require a running environment and administrator credentials. Use `--credentials` or `KEEL_CREDENTIALS_FILE` to select that private file. Use `--service` when a project has multiple SQL services.

## Starting an environment

Run `keel` without a command to start a standalone local environment.

Start an environment with the defaults:

```sh
keel
```

Choose another port and data directory:

```sh
keel --port 4223 --data ./data-4223
```

Enable embedded SQL:

```sh
keel --sql --credentials credentials.json --data ./data-sql
```

The default address is `127.0.0.1:4222`, and the default data directory is `./data`. Port `-1` chooses a free port. Give each running environment its own data directory.

`KEEL_TOKEN` sets the shared token for local clients. `--credentials` selects per-service credentials. Services connect to an externally started environment through `KEEL_URL`.

CLI v0.2.2+ includes standalone SQL. Clustering requires a native build from the checkout.

## Getting help

Use `--help` to list commands or inspect a command's options.

```sh
keel --help
keel init --help
keel use --help
keel dev --help
keel upgrade --help
```

Help and version commands do not need a running Keel environment.
