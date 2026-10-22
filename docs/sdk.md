# Keel SDK components

[SDK guide](sdk.md) · [CLI reference](cli.md) · [Development guide](development.md)

> Keel is an early prototype. These examples use SDK v0.2.1 and CLI v0.2.2 or later.

Keel connects TypeScript and Go services and provides storage, events, and background jobs through their SDKs. The CLI runs the local environment and restarts your service when its source changes. This guide uses those tools to build a product catalog with categories, prices, and stock, plus a worker that updates an index.

## Contents

- [Install Keel and build a product catalog](#install-keel-and-build-a-product-catalog)
- [Startup troubleshooting](#startup-troubleshooting)
- [Follow one product through a handler](#follow-one-product-through-a-handler)
- [Lifecycle](#lifecycle)
- [Typed services and clients](#typed-services-and-clients)
- [Actions and input validation](#actions-and-input-validation)
- [HTTP routes](#http-routes)
- [Key value storage](#key-value-storage)
- [Collections](#collections)
- [Events and subscriptions](#events-and-subscriptions)
- [Jobs and workers](#jobs-and-workers)
- [Contracts and version lifecycle](#contracts-and-version-lifecycle)
- [Handler context and errors](#handler-context-and-errors)

## Install Keel and build a product catalog

On Linux amd64/arm64, Apple Silicon macOS, or Windows with WSL, install the CLI from its GitHub release:

```sh
curl -fsSL https://github.com/marcelsud/keel/releases/latest/download/install.sh | sh
```

Open a new terminal after installation. The release includes the contract generators. See the [CLI reference](cli.md#updating-keel-cli) for upgrades.

Check that the installed CLI is v0.2.2 or later:

```sh
keel --version
```

Use Bun 1.3+ for TypeScript or Go 1.26+ for Go. Create an empty directory and initialize the project:

```sh
mkdir catalog
cd catalog
keel init
```

Choose the current directory, your language, and the `empty` starter. Keel installs the SDK dependencies. Add the catalog service and client files below.

TypeScript setup:

```text
Project directory (current directory or a new path) [.]:
Language (typescript/go) [typescript]:
Starter (empty/json/protobuf) [json]: empty
```

Go setup:

```text
Project directory (current directory or a new path) [.]:
Language (typescript/go) [typescript]: go
Starter (empty/json/protobuf) [json]: empty
```

Create the client directory, replace the starter service, and add the client file. Both versions create the same product with a category, price in cents, stock quantity, and active status.

Create `src/` for the TypeScript files:

```sh
mkdir -p src
```

Create `cmd/client/` for the Go client:

```sh
mkdir -p cmd/client
```

### Catalog service, client, and startup configuration

#### TypeScript, `src/service.ts`

```ts
import { createKeel, ValidationError } from "@marcelsud/keel.ts";

const app = await createKeel({
  name: "catalog",
  url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

const createProduct = app.action({
  name: "createProduct",
  args: app.schema.object({
    name: app.schema.string(),
    category: app.schema.string(),
    priceCents: app.schema.integer(),
    stock: app.schema.integer(),
    active: app.schema.boolean(),
  }),
  async handler(ctx, input) {
    if (!input.name.trim() || !input.category.trim() ||
        input.priceCents < 0 || input.priceCents > 2_147_483_647 ||
        input.stock < 0 || input.stock > 2_147_483_647) {
      throw new ValidationError("invalid product");
    }
    const id = await ctx.collection.insert("products", input);
    return { id, ...input };
  },
});

await app.start();
console.log("catalog ready");
```

#### Go, `main.go`

```go
package main

import (
  "context"
  "encoding/json"
  "fmt"
  "log"
  "math"
  "os"
  "os/signal"
  "strings"
  "syscall"

  "github.com/marcelsud/keel/sdk"
)

func main() {
  app, err := sdk.Connect(sdk.Options{
    Name: "catalog", URL: os.Getenv("KEEL_URL"),
    Token: os.Getenv("KEEL_TOKEN"),
  })
  if err != nil { log.Print(err); return }
  defer app.Stop()

  err = app.Action("createProduct", func(
    ctx *sdk.Context, raw json.RawMessage,
  ) (any, error) {
    var input map[string]any
    if json.Unmarshal(raw, &input) != nil {
      return nil, &sdk.Error{Code: 400, Message: "invalid product"}
    }
    name, _ := input["name"].(string)
    category, _ := input["category"].(string)
    priceCents, priceOK := input["priceCents"].(float64)
    stock, stockOK := input["stock"].(float64)
    active, activeOK := input["active"].(bool)
    if strings.TrimSpace(name) == "" || strings.TrimSpace(category) == "" ||
      !priceOK || math.Trunc(priceCents) != priceCents ||
      priceCents < 0 || priceCents > 2_147_483_647 ||
      !stockOK || math.Trunc(stock) != stock || stock < 0 || stock > 2_147_483_647 ||
      !activeOK {
      return nil, &sdk.Error{Code: 400, Message: "invalid product"}
    }
    product := map[string]any{
      "name": name, "category": category, "priceCents": priceCents,
      "stock": stock, "active": active,
    }
    id, err := ctx.Collection.Insert(ctx, "products", product)
    if err != nil { return nil, err }
    product["id"] = id
    return product, nil
  })
  if err != nil { log.Print(err); return }

  ctx, stop := signal.NotifyContext(
    context.Background(), os.Interrupt, syscall.SIGTERM,
  )
  defer stop()
  if err := app.Start(ctx); err != nil { log.Print(err); return }
  fmt.Println("catalog ready")
  <-ctx.Done()
}
```

#### TypeScript, `src/client.ts`

```ts
import { createKeel } from "@marcelsud/keel.ts";

const app = await createKeel({
  name: "catalog-client",
  url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

try {
  const product = await app.call<{
    id: string; name: string; category: string;
    priceCents: number; stock: number; active: boolean;
  }>("catalog", "createProduct", {
    name: "Trail Backpack", category: "outdoor",
    priceCents: 7990, stock: 12, active: true,
  });
  console.log(`${product.name} | ${product.category} | ${product.priceCents} cents | stock ${product.stock} | active ${product.active}`);
} finally {
  await app.stop();
}
```

#### Go, `cmd/client/main.go`

```go
package main

import (
  "context"
  "fmt"
  "log"
  "os"

  "github.com/marcelsud/keel/sdk"
)

func main() {
  app, err := sdk.Connect(sdk.Options{
    Name: "catalog-client", URL: os.Getenv("KEEL_URL"),
    Token: os.Getenv("KEEL_TOKEN"),
  })
  if err != nil { log.Print(err); return }
  defer app.Stop()
  ctx := context.Background()

  var product struct {
    ID, Name, Category string
    PriceCents, Stock int32
    Active bool
  }
  err = app.Call(ctx, "catalog", "createProduct", map[string]any{
    "name": "Trail Backpack", "category": "outdoor",
    "priceCents": 7990, "stock": 12, "active": true,
  }, &product)
  if err != nil { log.Print(err); return }
  fmt.Printf("%s | %s | %d cents | stock %d | active %t\n",
    product.Name, product.Category, product.PriceCents, product.Stock, product.Active,
  )
}
```

#### TypeScript, `keel.toml`

```toml
command = ["bun", "run", "src/service.ts"]

[[targets]]
language = "typescript"
out = "src/generated"
```

#### Go, `keel.toml`

```toml
command = ["go", "run", "."]
```

Start the catalog in the first terminal. `keel dev` starts the local environment and restarts the service after source changes:

```sh
keel dev
```

The message `no protobuf contracts; skipping generation` is expected for this JSON example. Wait for `catalog ready`. In a second terminal, run the client from the same project directory:

Run the TypeScript client:

```sh
bun run src/client.ts
```

Run the Go client:

```sh
go run ./cmd/client
```

Both clients print:

```text
Trail Backpack | outdoor | 7990 cents | stock 12 | active true
```

The action saves the product in `products` with a generated ID. Each client run creates a new product. The `priceCents` field stores an integer number of cents. Both `priceCents` and `stock` accept integers between 0 and 2,147,483,647. Invalid input returns a 400 service error.

In TypeScript, run `bun run check` to type-check the service and client. The schema validates JSON types, and the handler checks the catalog rules. The client result annotation does not validate the response at runtime.

In Go, run `go build ./...` to check the service and client. The handler validates JSON types and catalog rules before saving. The client decodes the response into a Go struct.

The reference snippets demonstrate the same operations in both languages. Run each scenario separately. Put handler registrations before `app.start()` or `app.Start(ctx)`. The lifecycle example marks the insertion point.

New projects use `keel.toml`. Keel also accepts `keel.json`, but reports an ambiguity error if both files exist. Run one `keel dev` process per project. Ctrl-C stops the service and local environment.

CLI v0.2.2+ includes embedded SQL. Follow the [SQL setup](development.md#use-sql-locally-prototype) for migrations and service credentials. SQL transaction callbacks cover one service's database. Collection writes, events, and queue operations commit separately.

## Startup troubleshooting

| Message or symptom | Next step |
| --- | --- |
| `no protobuf contracts; skipping generation` | Expected for the starter. Keep the process running and wait for `catalog ready`. You can add protobuf later. |
| `local environment is already running` | Keep the first `keel dev` running. Run the TypeScript client with `bun run src/client.ts` or the Go client with `go run ./cmd/client` in the second terminal. Stop the first process with Ctrl-C before starting it again. |
| `Error listening on port` | Another environment or application owns that port. Stop it, or use a different port for a separate project as shown below. A different port does not let two environments share one project's data directory. |
| `unknown command "init"` | Rerun the CLI installer, then check `keel --version`. Use v0.2.1 or later. |
| `app.schema` is missing in TypeScript | Upgrade the project's SDK with `bun add @marcelsud/keel.ts@0.2.1`, then run `bun run check`. Updating the CLI alone does not change an existing project's dependencies. |
| `set command in keel.toml or keel.json` | `keel init` creates the command for new projects. For an existing project, set `command = ["bun", "run", "src/service.ts"]` for TypeScript or `command = ["go", "run", "."]` for Go in `keel.toml`. |
| `both keel.toml and keel.json` | Choose one configuration file and remove the other. Keel reports an ambiguity error when both exist. |

### Use a different port or share an environment with another project

For a separate project with its own environment, start on a different port:

```sh
keel dev --port 4223
```

Set the same URL when calling it from the second terminal:

Run the TypeScript client on port 4223:

```sh
KEEL_URL=nats://127.0.0.1:4223 bun run src/client.ts
```

Run the Go client on port 4223:

```sh
KEEL_URL=nats://127.0.0.1:4223 go run ./cmd/client
```

`keel dev --port -1` chooses a free port. Copy the URL from `keel ready` into the client's `KEEL_URL`.

To share an already running environment instead, run this from another service project:

```sh
KEEL_URL=nats://127.0.0.1:4222 keel dev
```

`KEEL_URL` makes Keel connect your service to that environment. Each service still has its own name and startup command.

## Follow one product through a handler

The handler saves the product, publishes `product.created`, then enqueues `catalog.reindex`. Each awaited call commits before the next call starts.

| Scenario | Product saved | Event published | Indexing job queued |
| --- | --- | --- | --- |
| All operations succeed | Yes | Yes | Yes |
| Publishing the event fails | Yes | No | Skipped |
| Enqueueing the job fails | Yes | Yes | No |

A later failure leaves earlier writes in place. These operations do not form one transaction.

## Lifecycle

Connect the catalog with `createKeel()` in TypeScript or `sdk.Connect()` in Go. Register its handlers before starting the app.

### Service entry point

#### TypeScript, `src/service.ts`

```ts
import { createKeel } from "@marcelsud/keel.ts";

const app = await createKeel({
  name: "catalog", url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

// Register the handlers from the selected example here.
await app.start();
console.log("catalog ready");
```

#### Go, `main.go`

```go
package main

import (
  "context"
  "fmt"
  "log"
  "os"
  "os/signal"
  "syscall"
  "github.com/marcelsud/keel/sdk"
)

func main() {
  app, err := sdk.Connect(sdk.Options{
    Name: "catalog", URL: os.Getenv("KEEL_URL"),
    Token: os.Getenv("KEEL_TOKEN"),
  })
  if err != nil { log.Print(err); return }
  defer app.Stop()
  ctx, stop := signal.NotifyContext(
    context.Background(), os.Interrupt, syscall.SIGTERM,
  )
  defer stop()

  // Register the handlers from the selected example here.
  if err := app.Start(ctx); err != nil { log.Print(err); return }
  fmt.Println("catalog ready")
  <-ctx.Done()
}
```

Register actions, typed services, routes, subscriptions, and workers before starting the app.

TypeScript's `start()` handles SIGINT and SIGTERM. Go uses a signal context and calls `Stop()` when the process exits. Both examples drain active work before closing the connection.

A client that only calls APIs does not need to start a service. Close its connection with `app.stop()` or `app.Stop()` after the calls finish.

## Typed services and clients

Generate the catalog contract for both languages so handlers and clients use the same product fields. Create both contract files below in the catalog project, then run `keel generate`. TypeScript generation requires Node.js 22+ on your PATH.

### Create the contract and generate the bindings

Write `proto/catalog/v2/catalog.proto`:

```proto
syntax = "proto3";
package catalog.v2;

service CatalogService {
  rpc CreateProduct(CreateProductRequest) returns (Product);
}
message CreateProductRequest {
  string name = 1;
  string category = 2;
  int32 price_cents = 3;
  int32 stock = 4;
  bool active = 5;
}
message Product {
  string id = 1;
  string name = 2;
  string category = 3;
  int32 price_cents = 4;
  int32 stock = 5;
  bool active = 6;
}
```

Write `proto/catalog/v1/catalog.proto`:

```proto
syntax = "proto3";
package catalog.v1;

service CatalogService {
  rpc CreateProduct(CreateProductRequest) returns (Product);
}
message CreateProductRequest {
  string name = 1;
  string category = 2;
  int32 price_cents = 3;
  int32 stock = 4;
  bool active = 5;
}
message Product {
  string id = 1;
  string name = 2;
  string category = 3;
  int32 price_cents = 4;
  int32 stock = 5;
  bool active = 6;
}
```

#### TypeScript

```ts
keel generate
```

#### Go

```go
keel generate
```

### Define the catalog handler

#### TypeScript

```ts
import { ValidationError, type ServiceImplementation }
  from "@marcelsud/keel.ts";
import { CatalogServiceContract as CatalogV2 }
  from "./generated/catalog/v2/catalog_keel.js";

const catalogHandler: ServiceImplementation<typeof CatalogV2.service> = {
  async createProduct(ctx, input) {
    if (!input.name.trim() || !input.category.trim() ||
        input.priceCents < 0 || input.stock < 0) {
      throw new ValidationError("invalid product");
    }
    const product = {
      name: input.name, category: input.category,
      priceCents: input.priceCents, stock: input.stock,
      active: input.active,
    };
    const id = await ctx.collection.insert("products", product);
    return { id, ...product };
  },
};
```

#### Go

```go
type catalogServer struct{}

func (catalogServer) CreateProduct(
  ctx *sdk.Context, input *catalogv2.CreateProductRequest,
) (*catalogv2.Product, error) {
  if strings.TrimSpace(input.GetName()) == "" ||
    strings.TrimSpace(input.GetCategory()) == "" ||
    input.GetPriceCents() < 0 || input.GetStock() < 0 {
    return nil, &sdk.Error{Code: 400, Message: "invalid product"}
  }
  product := map[string]any{
    "name": input.GetName(), "category": input.GetCategory(),
    "priceCents": input.GetPriceCents(), "stock": input.GetStock(),
    "active": input.GetActive(),
  }
  id, err := ctx.Collection.Insert(ctx, "products", product)
  if err != nil { return nil, err }
  return &catalogv2.Product{
    Id: id, Name: input.GetName(), Category: input.GetCategory(),
    PriceCents: input.GetPriceCents(), Stock: input.GetStock(),
    Active: input.GetActive(),
  }, nil
}
```

### Register the catalog handler

#### TypeScript

```ts
import { CatalogServiceContract as CatalogV2 }
  from "./generated/catalog/v2/catalog_keel.js";

app.service(CatalogV2, catalogHandler);
```

#### Go

```go
err = catalogv2.RegisterCatalogService(app, catalogServer{}, sdk.ContractOptions{})
if err != nil { log.Print(err); return }
```

### Create a product from a connected client

#### TypeScript

```ts
import { CatalogServiceContract as CatalogV2 }
  from "./generated/catalog/v2/catalog_keel.js";

const catalog = app.client(CatalogV2);
const product = await catalog.createProduct({
  name: "Trail Backpack", category: "outdoor",
  priceCents: 7990, stock: 12, active: true,
});
console.log(`${product.name} | ${product.category} | ${product.priceCents} cents | stock ${product.stock} | active ${product.active}`);
```

#### Go

```go
catalog := catalogv2.NewCatalogServiceClient(app)
product, err := catalog.CreateProduct(ctx, &catalogv2.CreateProductRequest{
  Name: "Trail Backpack", Category: "outdoor",
  PriceCents: 7990, Stock: 12, Active: true,
})
if err != nil { log.Print(err); return }
fmt.Printf("%s | %s | %d cents | stock %d | active %t\n",
  product.GetName(), product.GetCategory(), product.GetPriceCents(),
  product.GetStock(), product.GetActive(),
)
```

The examples implement v2. The v1 file supplies the contract for the retirement example. Both handlers reject blank names or categories and negative prices or stock before writing to `products`. Protobuf `int32` fields set the same numeric range in both languages.

In Go, declare `catalogServer` and its method outside `main()`. Call `RegisterCatalogService()` inside `main()`, before `Start()`. Keel supports only unary RPC.

## Actions and input validation

`createProduct` accepts JSON requests and returns a product. This action does not require generated bindings.

### Register createProduct

#### TypeScript

```ts
import { ValidationError } from "@marcelsud/keel.ts";

const createProduct = app.action({
  name: "createProduct",
  args: app.schema.object({
    name: app.schema.string(),
    category: app.schema.string(),
    priceCents: app.schema.integer(),
    stock: app.schema.integer(),
    active: app.schema.boolean(),
  }),
  async handler(ctx, input) {
    if (!input.name.trim() || !input.category.trim() ||
        input.priceCents < 0 || input.priceCents > 2_147_483_647 ||
        input.stock < 0 || input.stock > 2_147_483_647) {
      throw new ValidationError("invalid product");
    }
    const id = await ctx.collection.insert("products", input);
    return { id, ...input };
  },
});
```

#### Go

```go
err = app.Action("createProduct", func(
  ctx *sdk.Context, raw json.RawMessage,
) (any, error) {
  var input map[string]any
  if json.Unmarshal(raw, &input) != nil {
    return nil, &sdk.Error{Code: 400, Message: "invalid product"}
  }
  name, _ := input["name"].(string)
  category, _ := input["category"].(string)
  priceCents, priceOK := input["priceCents"].(float64)
  stock, stockOK := input["stock"].(float64)
  active, activeOK := input["active"].(bool)
  if strings.TrimSpace(name) == "" || strings.TrimSpace(category) == "" ||
    !priceOK || math.Trunc(priceCents) != priceCents ||
    priceCents < 0 || priceCents > 2_147_483_647 ||
    !stockOK || math.Trunc(stock) != stock || stock < 0 || stock > 2_147_483_647 ||
    !activeOK {
    return nil, &sdk.Error{Code: 400, Message: "invalid product"}
  }
  product := map[string]any{
    "name": name, "category": category, "priceCents": priceCents,
    "stock": stock, "active": active,
  }
  id, err := ctx.Collection.Insert(ctx, "products", product)
  if err != nil { return nil, err }
  product["id"] = id
  return product, nil
})
if err != nil { log.Print(err); return }
```

### Create a product from a connected client

#### TypeScript

```ts
const product = await app.call<{
  id: string; name: string; category: string;
  priceCents: number; stock: number; active: boolean;
}>("catalog", "createProduct", {
  name: "Trail Backpack", category: "outdoor",
  priceCents: 7990, stock: 12, active: true,
});
console.log(`${product.name} | ${product.category} | ${product.priceCents} cents | stock ${product.stock} | active ${product.active}`);
```

#### Go

```go
var product struct {
  ID, Name, Category string
  PriceCents, Stock int32
  Active bool
}
err = app.Call(ctx, "catalog", "createProduct", map[string]any{
  "name": "Trail Backpack", "category": "outdoor",
  "priceCents": 7990, "stock": 12, "active": true,
}, &product)
if err != nil { log.Print(err); return }
fmt.Printf("%s | %s | %d cents | stock %d | active %t\n",
  product.Name, product.Category, product.PriceCents, product.Stock, product.Active,
)
```

The tutorial already registers `createProduct`. Replace that registration when you try this example.

Both handlers accept `name`, `category`, `priceCents`, `stock`, and `active`. They ignore extra fields and reject missing or invalid values with a 400 error. Prices and stock must be integers from 0 to 2,147,483,647.

TypeScript's `app.schema` validates JSON types. Go validates the decoded values. Use a typed service when callers need a shared contract.

## HTTP routes

HTTP routing requires SDK v0.2.1 or later. Add routes so browser and external clients can create and retrieve catalog products. Both languages use the same paths and JSON responses.

### Enable HTTP when connecting

#### TypeScript

```ts
import { createKeel } from "@marcelsud/keel.ts";
import { node } from "@marcelsud/keel.ts/http/node";

const app = await createKeel({
  name: "catalog", url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
  http: node({ hostname: "127.0.0.1", port: 3000 }),
});
```

#### Go

```go
app, err := sdk.Connect(sdk.Options{
  Name: "catalog", URL: os.Getenv("KEEL_URL"),
  Token: os.Getenv("KEEL_TOKEN"),
  HTTP: &sdk.HTTPOptions{Address: "127.0.0.1:3000"},
})
if err != nil { log.Print(err); return }
defer app.Stop()
```

### Register the catalog routes

#### TypeScript

```ts
import { ServiceError, type HttpContext } from "@marcelsud/keel.ts";

app.route("GET", "/health", () => ({ ok: true }));
app.route("POST", "/products", {
  action: (input: unknown) => app.call("catalog", "createProduct", input),
  status: 201,
  output: (product: unknown) => ({ data: product }),
});
app.route("GET", "/products/:id", async ({ params }: HttpContext) => {
  const product = await app.collection.get("products", params.id);
  if (product === null) throw new ServiceError(404, "product not found");
  return { data: product };
});
```

#### Go

```go
err = app.Route("GET", "/health", func(_ *sdk.HTTPContext) (any, error) {
  return map[string]bool{"ok": true}, nil
})
if err != nil { log.Print(err); return }
err = app.Route("POST", "/products", sdk.HTTPRoute{
  Action: "createProduct", Status: 201,
  Output: func(product any, _ *sdk.HTTPContext) (any, error) {
    return map[string]any{"data": product}, nil
  },
})
if err != nil { log.Print(err); return }
err = app.Route("GET", "/products/:id", func(http *sdk.HTTPContext) (any, error) {
  var product map[string]any
  found, err := app.Collection.Get(http.Request.Context(), "products", http.Params["id"], &product)
  if err != nil { return nil, err }
  if !found { return nil, &sdk.Error{Code: 404, Message: "product not found"} }
  return map[string]any{"data": product}, nil
})
if err != nil { log.Print(err); return }
```

Replace the tutorial connection options with the HTTP options shown in this section. Keep `createProduct` registered and add the routes before starting the app. Both examples listen on `127.0.0.1:3000`.

| Route | Success status | Response |
| --- | --- | --- |
| `GET /health` | 200 | `{"ok": true}` |
| `POST /products` | 201 | `{"data": product}` |
| `GET /products/:id` | 200 | `{"data": product}` |

An invalid product request returns 400. A missing product returns 404. The SDKs handle HEAD and OPTIONS. They return 404 for unknown paths and 405 when the path matches but the method does not.

Output mapping runs after the action writes the product. A mapping failure does not undo that write.

## Key value storage

Store the catalog's default category under `defaultCategory` in the `catalogSettings` namespace.

### Write, read, and delete the default category

#### TypeScript

```ts
const settings = app.kv.namespace("catalogSettings");
await settings.set("defaultCategory", { category: "outdoor" });
const value = await settings.get<{ category: string }>("defaultCategory");
if (value !== undefined) console.log(value.category);
const removed = await settings.delete("defaultCategory");
console.log(removed);
```

#### Go

```go
settings := app.KV.Namespace("catalogSettings")
err = settings.Set(ctx, "defaultCategory", map[string]string{"category": "outdoor"})
if err != nil { log.Print(err); return }
var value struct { Category string }
found, err := settings.Get(ctx, "defaultCategory", &value)
if err != nil { log.Print(err); return }
if found { fmt.Println(value.Category) }
removed, err := settings.Delete(ctx, "defaultCategory")
if err != nil { log.Print(err); return }
fmt.Println(removed)
```

A missing key returns `undefined` in TypeScript and `found=false` in Go. A stored JSON `null` is a separate value. Services that choose the same namespace share its keys. A namespace does not restrict access.

For concurrent inventory changes, read the value and revision with `getEntry()` or `GetEntry()`, then write with `compareAndSet()` or `CompareAndSet()`. Reread after a 409 conflict. This protects one KV value. Collection updates and separate writes still commit independently.

## Collections

Use the `products` collection to create and list products, read their categories and stock, update inventory, and delete records.

### Create, list, read, update inventory, and delete

#### TypeScript

```ts
await app.collection.create("products", "backpack-001", {
  name: "Trail Backpack", category: "outdoor",
  priceCents: 7990, stock: 12, active: true,
});
const products = await app.collection.list<{ name: string }>("products");
console.log(products.map(product => product.name).join(", "));
const product = await app.collection.get<{
  category: string; stock: number;
}>("products", "backpack-001");
if (product !== null) console.log(product.category, product.stock);
const updated = await app.collection.update("products", "backpack-001", {
  stock: 8, active: false,
});
const removed = await app.collection.delete("products", "backpack-001");
console.log(updated, removed);
```

#### Go

```go
_, err = app.Collection.Create(ctx, "products", "backpack-001", map[string]any{
  "name": "Trail Backpack", "category": "outdoor",
  "priceCents": 7990, "stock": 12, "active": true,
})
if err != nil { log.Print(err); return }
var products []struct { Name string }
if err := app.Collection.List(ctx, "products", &products); err != nil {
  log.Print(err); return
}
names := make([]string, len(products))
for i, product := range products { names[i] = product.Name }
fmt.Println(strings.Join(names, ", "))
var product struct { Category string; Stock int32 }
found, err := app.Collection.Get(ctx, "products", "backpack-001", &product)
if err != nil { log.Print(err); return }
if found { fmt.Println(product.Category, product.Stock) }
updated, err := app.Collection.Update(ctx, "products", "backpack-001", map[string]any{
  "stock": 8, "active": false,
})
if err != nil { log.Print(err); return }
removed, err := app.Collection.Delete(ctx, "products", "backpack-001")
if err != nil { log.Print(err); return }
fmt.Println(updated, removed)
```

Start this scenario with a fresh data directory. The examples create `backpack-001`, keep its category and price, set `stock` to 8 and `active` to false, then delete it.

`create()` and `Create()` return 409 if the ID exists. Updates merge top-level fields. The stock update assigns a value, so it does not prevent concurrent writers from overwriting each other. A missing document returns `null` in TypeScript and `found=false` in Go.

Listings return live documents in ID order, with a limit of 1000 documents or 512 KiB. Filter by category in your application, or use SQL for queries that need filtering.

## Events and subscriptions

Publish `product.created` to tell subscribers that a product was created. The catalog subscription enqueues a job to update the index.

### Register the product-created subscription

#### TypeScript

```ts
import { ValidationError } from "@marcelsud/keel.ts";

app.subscription<{ id: string }>("product.created", async (ctx, event) => {
  if (typeof event?.id !== "string" || !event.id.trim()) {
    throw new ValidationError("product id is required");
  }
  await ctx.queue.enqueue("catalog.reindex", { id: event.id });
}, { name: "catalogReindexOnCreated" });
```

#### Go

```go
err = app.Subscription("product.created", "catalogReindexOnCreated",
  func(ctx *sdk.Context, raw json.RawMessage) error {
    var event struct { ID string }
    if json.Unmarshal(raw, &event) != nil || strings.TrimSpace(event.ID) == "" {
      return &sdk.Error{Code: 400, Message: "product id is required"}
    }
    return ctx.Queue.Enqueue(ctx, "catalog.reindex", map[string]string{"id": event.ID})
  },
)
if err != nil { log.Print(err); return }
```

### Publish the product-created event

#### TypeScript

```ts
await app.pubsub.publish("product.created", { id: "backpack-001" });
```

#### Go

```go
err = app.Pubsub.Publish(ctx, "product.created", map[string]string{"id": "backpack-001"})
if err != nil { log.Print(err); return }
```

Both examples publish `{"id":"backpack-001"}`. The `catalogReindexOnCreated` subscription enqueues `catalog.reindex` for that product.

Each subscription name receives its own copy of the event. Instances with the same name share deliveries. Keel retries failed handlers, so consumers must tolerate duplicate jobs. Return normally to acknowledge a delivery.

Saving the product, publishing the event, and enqueueing the job commit separately. The failure scenarios above show which writes remain.

## Jobs and workers

Read a product and copy its name, category, stock, and active status into the `catalogIndex` KV namespace. Run this job after product creation or an inventory change.

### Register the catalog indexing worker

#### TypeScript

```ts
import { ValidationError } from "@marcelsud/keel.ts";

app.worker<{ id: string }>("catalog.reindex", async (ctx, job) => {
  if (typeof job?.id !== "string" || !job.id.trim()) {
    throw new ValidationError("product id is required");
  }
  const index = ctx.kv.namespace("catalogIndex");
  const product = await ctx.collection.get<{
    name: string; category: string; stock: number; active: boolean;
  }>("products", job.id);
  if (product === null) {
    await index.delete(job.id);
    return;
  }
  await index.set(job.id, {
    name: product.name, category: product.category,
    stock: product.stock, active: product.active,
  });
});
```

#### Go

```go
err = app.Worker("catalog.reindex", func(ctx *sdk.Context, raw json.RawMessage) error {
  var job struct { ID string }
  if json.Unmarshal(raw, &job) != nil || strings.TrimSpace(job.ID) == "" {
    return &sdk.Error{Code: 400, Message: "product id is required"}
  }
  index := ctx.KV.Namespace("catalogIndex")
  var product struct {
    Name, Category string
    Stock int32
    Active bool
  }
  found, err := ctx.Collection.Get(ctx, "products", job.ID, &product)
  if err != nil { return err }
  if !found {
    _, err := index.Delete(ctx, job.ID)
    return err
  }
  return index.Set(ctx, job.ID, map[string]any{
    "name": product.Name, "category": product.Category,
    "stock": product.Stock, "active": product.Active,
  })
})
if err != nil { log.Print(err); return }
```

### Queue a catalog indexing job

#### TypeScript

```ts
await app.queue.enqueue("catalog.reindex", { id: "backpack-001" });
```

#### Go

```go
err = app.Queue.Enqueue(ctx, "catalog.reindex", map[string]string{"id": "backpack-001"})
if err != nil { log.Print(err); return }
```

Both workers read `products` by ID and write the same `name`, `category`, `stock`, and `active` fields under that ID in `catalogIndex`. If the product is missing, the worker deletes its old index entry and acknowledges the job.

The worker copies stored values instead of decrementing stock. Repeating the job for an unchanged product writes the same index entry.

Keel retries failed handlers until they acknowledge the job. Delivery is at least once, and workers share the queue. Keel has no automatic dead-letter queue or scheduled jobs.

## Contracts and version lifecycle

Fetch the catalog v2 contract, then retire v1 while keeping its schema available to clients.

### Fetch the catalog v2 contract

#### TypeScript

```ts
const contract = await app.fetchContract("catalog.v2.CatalogService");
console.log(contract.service);
console.log(contract.state);
console.log(contract.sha256);
```

#### Go

```go
contract, err := app.FetchContract(ctx, "catalog.v2.CatalogService")
if err != nil { log.Print(err); return }
fmt.Println(contract.Service)
fmt.Println(contract.State)
fmt.Println(contract.SHA256)
```

### Retire catalog v1 and point clients to v2

#### TypeScript

```ts
import { CatalogServiceContract as CatalogV1 }
  from "./generated/catalog/v1/catalog_keel.js";

app.service(CatalogV1, undefined, {
  state: "retired", replacement: "catalog.v2.CatalogService",
});
```

#### Go

```go
err = catalogv1.RegisterCatalogService(app, nil, sdk.ContractOptions{
  State: sdk.Retired, Replacement: "catalog.v2.CatalogService",
})
if err != nil { log.Print(err); return }
```

Generate the v1 and v2 contracts from the typed-service example. Both implementations fetch `catalog.v2.CatalogService` and register v1 as `retired`, with v2 as its replacement. The v2 handler continues to serve requests.

Retired calls return 410. Clients can still fetch the v1 schema. Keel supports the `active`, `deprecated`, and `retired` states. The alpha/beta/stable lifecycle and dated EOL notices are planned.

## Handler context and errors

Action, subscription, and worker handlers receive a context with storage and messaging APIs. HTTP handlers receive the request, parameters, query, and body. Call an action from an HTTP route to reuse service logic.

### Inside a handler

In TypeScript, handlers can use `ctx.kv`, `ctx.collection`, `ctx.pubsub`, `ctx.queue`, `ctx.client()`, `ctx.call()`, and `ctx.log`.

In Go, handlers can use `ctx.KV`, `ctx.Collection`, `ctx.Pubsub`, `ctx.Queue`, `ctx.Call()`, and `ctx.Log`. Generated typed clients also accept the handler context.

### Service errors

In TypeScript, throw `ValidationError` to return 400, or `ServiceError(code, message)` to return another service error. Clients receive a `ServiceError`. Unexpected handler errors return 500.

In Go, return `&sdk.Error{Code: 400, Message: "..."}` for an explicit service error. Callers can inspect it with `errors.As`. Unexpected handler errors return 500.

### Requests and shutdown

TypeScript requests use a 5-second timeout by default. Set `timeout` to change it. Await `app.stop()` to drain active handlers and deliveries.

In Go, pass a `context.Context` to calls. Requests use a 5-second timeout if the context has no deadline. `app.Stop()` drains active handlers and deliveries.

### What is available today

Keel supports typed unary RPC, storage operations, events, workers, and concurrent API versions. GitHub contract installation, dated EOL notices, and the alpha/beta/stable lifecycle are planned.
