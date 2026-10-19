import assert from "node:assert/strict";
import { after, before, test } from "node:test";
import { execFile, execFileSync, spawn } from "node:child_process";
import { mkdtemp, rm, readFile, writeFile, symlink } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import { once } from "node:events";
import { createKeel, schema, ValidationError } from "../dist/index.js";
import { NotesServiceContract as NotesV1 } from "../dist/gen/notes/v1/notes_keel.js";
import { NotesServiceContract as NotesV2 } from "../dist/gen/notes/v2/notes_keel.js";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
const apps = [];
const execute = promisify(execFile);
let directory, server, url, binary, goExample, goPlugin;

before(async () => {
  directory = await mkdtemp(join(tmpdir(), "keel-ts-"));
  binary = process.env.KEEL_BINARY ?? join(directory, "keel");
  if (!process.env.KEEL_BINARY) execFileSync("go", ["build", "-buildvcs=false", "-o", binary, "./cmd/keel"], { cwd: root });
  goExample = process.env.KEEL_GO_EXAMPLE ?? join(directory, "notes-go");
  if (!process.env.KEEL_GO_EXAMPLE) execFileSync("go", ["build", "-buildvcs=false", "-o", goExample, "./examples/contracts/go"], { cwd: root });
  goPlugin = process.env.KEEL_GO_PLUGIN ?? join(directory, "protoc-gen-go");
  if (!process.env.KEEL_GO_PLUGIN) execFileSync("go", ["build", "-o", goPlugin, "google.golang.org/protobuf/cmd/protoc-gen-go"], { cwd: root });
  server = spawn(binary, ["-data", join(directory, "data"), "-port", "-1"], {
    env: { ...process.env, KEEL_TOKEN: "typescript-test" }, stdio: ["ignore", "ignore", "pipe"],
  });
  url = await new Promise((resolve, reject) => {
    let output = "";
    const timer = setTimeout(() => reject(new Error(`core did not start: ${output}`)), 10_000);
    server.stderr.on("data", (chunk) => {
      output += chunk;
      const match = output.match(/url=(nats:\/\/\S+)/);
      if (match) { clearTimeout(timer); resolve(match[1]); }
    });
    server.once("error", (error) => { clearTimeout(timer); reject(error); });
    server.once("exit", () => { clearTimeout(timer); reject(new Error(output)); });
  });
});

after(async () => {
  await Promise.all(apps.map((app) => app.stop()));
  if (server && server.exitCode === null) { const exited = once(server, "exit"); server.kill("SIGTERM"); await exited; }
  if (directory) await rm(directory, { recursive: true, force: true });
});

async function app(name) {
  const instance = await createKeel({ name, url, token: "typescript-test" });
  apps.push(instance);
  return instance;
}

function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}

async function delivered(promise) {
  let timer;
  try { return await Promise.race([promise, new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("delivery timed out")), 8000); })]); }
  finally { clearTimeout(timer); }
}

test("named actions validate requests and return JSON", async () => {
  const service = await app("typescript-actions");
  const echo = service.action({ name: "echo", args: { parse(input) {
    if (!input || typeof input.body !== "string") throw new Error("body must be a string");
    return input;
  } }, handler: (_ctx, input) => input });
  assert.throws(() => service.action({ name: "echo", handler: () => null }), /duplicate/);
  await service.start();
  assert.deepEqual(await echo({ body: "hello" }), { body: "hello" });
  await assert.rejects(service.call("typescript-actions", "echo", {}), (error) => error.code === 400);
  assert.throws(() => service.worker("late", () => {}), /before start/);
  await service.stop();
  await assert.rejects(service.start(), /stopped/);
});

test("app.schema validates remote action inputs before the handler runs", async () => {
  const service = await app("typescript-schema");
  assert.equal(service.schema, schema);
  let handled = 0;
  const echo = service.action({
    name: "echo",
    args: service.schema.object({
      user: service.schema.object({ name: service.schema.string() }),
      tags: service.schema.string().array().optional(),
    }),
    handler: (_ctx, input) => { handled++; return input; },
  });
  await service.start();
  const caller = await app("typescript-schema-client");
  assert.deepEqual(await caller.call("typescript-schema", "echo", { user: { name: "Ada", extra: true } }), { user: { name: "Ada" } });
  assert.deepEqual(await echo({ user: { name: "Grace" }, tags: ["hello"] }), { user: { name: "Grace" }, tags: ["hello"] });
  for (const [input, message] of [
    [{ user: {} }, "value.user.name is required"],
    [{ user: { name: 42 } }, "value.user.name must be a string"],
    [{ user: { name: "Ada" }, tags: [1] }, "value.tags[0] must be a string"],
  ]) {
    await assert.rejects(caller.call("typescript-schema", "echo", input), (error) => error.code === 400 && error.message.includes(message));
  }
  assert.equal(handled, 2);
});

test("KV preserves missing, null, namespace isolation, and deletion results", async () => {
  const service = await app("typescript-kv");
  assert.equal(await service.kv.get("missing"), undefined);
  await service.kv.set("nullable", null);
  assert.equal(await service.kv.get("nullable"), null);
  const settings = service.kv.namespace("settings.日本語");
  await settings.set("theme.*", "dark");
  assert.equal(await settings.get("theme.*"), "dark");
  assert.equal(await service.kv.get("theme.*"), undefined);
  assert.equal(await settings.delete("theme.*"), true);
  assert.equal(await settings.delete("theme.*"), false);
  await assert.rejects(service.kv.set("bad", undefined), ValidationError);
  await assert.rejects(service.kv.get(""), (error) => error.code === 400);
});

test("collections enforce object documents and shallow patches", async () => {
  const service = await app("typescript-collections");
  const id = await service.collection.insert("notes", { body: "hello", nested: { a: 1 } });
  assert.match(id, /^[0-9a-f-]{36}$/);
  assert.equal(await service.collection.update("notes", id, { nested: { b: 2 } }), true);
  assert.deepEqual(await service.collection.get("notes", id), { id, body: "hello", nested: { b: 2 } });
  await assert.rejects(service.collection.insert("notes", { id: "forbidden" }), (error) => error.code === 400);
  await assert.rejects(service.collection.insert("notes", []), (error) => error.code === 400);
  assert.equal(await service.collection.delete("notes", id), true);
  assert.equal(await service.collection.get("notes", id), null);
  assert.equal(await service.collection.update("notes", id, {}), false);
});

test("writes survive a later handler failure", async () => {
  const service = await app("typescript-independent");
  const fail = service.action({ name: "fail", async handler(ctx) {
    await ctx.kv.set("before-failure", true);
    throw new Error("expected failure");
  } });
  await service.start();
  await assert.rejects(fail(null), (error) => error.code === 500);
  assert.equal(await service.kv.get("before-failure"), true);
});

test("named subscriptions fan out and workers retry failed deliveries", async () => {
  const first = await app("typescript-events-one");
  const second = await app("typescript-events-two");
  const eventOne = deferred(), eventTwo = deferred(), job = deferred();
  let attempts = 0;
  first.subscription("created", async (ctx, payload) => {
    eventOne.resolve(payload);
    await ctx.queue.enqueue("notify", payload);
  }, { name: "notify" });
  second.subscription("created", (_ctx, payload) => { eventTwo.resolve(payload); }, { name: "audit" });
  const worker = (_ctx, payload) => {
    attempts++;
    if (attempts === 1) throw new Error("retry delivery");
    job.resolve(payload);
  };
  first.worker("notify", worker);
  second.worker("notify", worker);
  await first.start(); await second.start();
  await first.pubsub.publish("created", { id: "1" });
  assert.deepEqual(await delivered(eventOne.promise), { id: "1" });
  assert.deepEqual(await delivered(eventTwo.promise), { id: "1" });
  assert.deepEqual(await delivered(job.promise), { id: "1" });
  await first.stop(); await second.stop();
  assert.equal(attempts, 2);
});

test("durable subscriptions resume after their SDK process stops", async () => {
  const first = await app("typescript-durable-first");
  first.subscription("offline", () => {}, { name: "stable" });
  await first.start(); await first.stop();
  const publisher = await app("typescript-durable-publisher");
  await publisher.pubsub.publish("offline", { saved: true });
  const second = await app("typescript-durable-second");
  const event = deferred();
  second.subscription("offline", (_ctx, payload) => { event.resolve(payload); }, { name: "stable" });
  await second.start();
  assert.deepEqual(await delivered(event.promise), { saved: true });
});

test("stop drains an active action and its storage calls", async () => {
  const service = await app("typescript-drain");
  const entered = deferred(), release = deferred();
  const slow = service.action({ name: "slow", async handler(ctx) {
    entered.resolve(); await release.promise;
    await ctx.kv.set("typescript-drained", true);
    return true;
  } });
  await service.start();
  const result = slow(null);
  await delivered(entered.promise);
  const stopped = service.stop();
  release.resolve();
  assert.equal(await result, true);
  await stopped;
});

function versioned(service, state = "deprecated") {
  service.service(NotesV1, {
    async createNote(ctx, input) {
      if (!input.body.trim()) throw new ValidationError("body is required");
      const note = await ctx.client(NotesV2).createNote({ content: { text: input.body } });
      return { id: note.id, body: note.content.text };
    },
  }, { state, replacement: NotesV2.service.typeName });
  service.service(NotesV2, {
    async createNote(ctx, input) {
      if (!input.content?.text.trim()) throw new ValidationError("content.text is required");
      const id = await ctx.collection.insert("versioned-notes", { text: input.content.text });
      return { id, content: input.content };
    },
  });
}

test("protobuf versions coexist and publish complete embedded contracts", async (t) => {
  const service = await app("typescript-protobuf");
  t.after(() => service.stop());
  versioned(service);
  assert.throws(() => service.service(NotesV1, {}), /missing handler/);
  await service.start();
  const caller = await app("typescript-protobuf-caller");
  const v1 = await caller.client(NotesV1).createNote({ body: "old request" });
  const v2 = await caller.client(NotesV2).createNote({ content: { text: "new request", title: "Title" } });
  assert.ok(v1.id); assert.equal(v1.body, "old request");
  assert.ok(v2.id); assert.deepEqual({ text: v2.content.text, title: v2.content.title }, { text: "new request", title: "Title" });
  await assert.rejects(caller.client(NotesV2).createNote({}), (error) => error.code === 400);
  const old = await caller.fetchContract(NotesV1.service.typeName);
  assert.equal(old.state, "deprecated"); assert.equal(old.replacement, NotesV2.service.typeName);
  const current = await caller.fetchContract(NotesV2.service.typeName);
  assert.equal(current.state, "active"); assert.equal(current.descriptorSet, NotesV2.schema);
});

test("retirement returns 410 while v2 and the archived v1 schema stay available", async (t) => {
  const service = await app("typescript-retired");
  t.after(() => service.stop());
  service.service(NotesV1, undefined, { state: "retired", replacement: NotesV2.service.typeName });
  service.service(NotesV2, { createNote: (_ctx, input) => ({ id: "active", content: input.content }) });
  await service.start();
  await assert.rejects(service.client(NotesV1).createNote({ body: "old" }), (error) => error.code === 410);
  assert.equal((await service.client(NotesV2).createNote({ content: { text: "new" } })).id, "active");
  const archived = await service.fetchContract(NotesV1.service.typeName);
  assert.equal(archived.state, "retired"); assert.equal(archived.descriptorSet, NotesV1.schema);
});

test("TypeScript clients call the runnable Go service example", async (t) => {
  const child = spawn(goExample, [], {
    env: { ...process.env, KEEL_URL: url, KEEL_TOKEN: "typescript-test", KEEL_RETIRE_V1: "0" },
    stdio: ["ignore", "ignore", "pipe"],
  });
  t.after(async () => {
    if (child.exitCode === null) { const done = once(child, "exit"); child.kill("SIGTERM"); await done; }
  });
  await new Promise((resolve, reject) => {
    let output = "";
    const timer = setTimeout(() => reject(new Error(`Go example did not start: ${output}`)), 8000);
    child.stderr.on("data", (chunk) => { output += chunk; if (output.includes("notes-go ready")) { clearTimeout(timer); resolve(); } });
    child.once("error", (error) => { clearTimeout(timer); reject(error); });
    child.once("exit", () => { clearTimeout(timer); reject(new Error(output)); });
  });
  const caller = await app("typescript-to-go");
  const first = await caller.client(NotesV1).createNote({ body: "TypeScript to Go v1" });
  assert.ok(first.id); assert.equal(first.body, "TypeScript to Go v1");
  const second = await caller.client(NotesV2).createNote({ content: { text: "TypeScript to Go v2", title: "Title" } });
  assert.ok(second.id); assert.equal(second.content.title, "Title");
  const schema = await caller.fetchContract(NotesV2.service.typeName);
  assert.equal(schema.descriptorSet, NotesV2.schema);
});

test("CLI pulls, pins, and compiles working Go and TypeScript clients", async (t) => {
  const service = await app("typescript-pull");
  t.after(() => service.stop());
  versioned(service);
  await service.start();
  const output = await mkdtemp(join(directory, "pulled-"));
  const environment = { ...process.env, KEEL_URL: url, KEEL_TOKEN: "typescript-test" };
  const args = ["pull", NotesV2.service.typeName, "--lang", "both", "--out", output,
    "--go-package-prefix", "example.com/pulled/go", "--go-plugin", goPlugin,
    "--es-plugin", join(root, "sdks/typescript/node_modules/.bin/protoc-gen-es")];
  await execute(binary, args, { env: environment });
  const lockPath = join(output, `${NotesV2.service.typeName}.lock.json`);
  const lock = JSON.parse(await readFile(lockPath, "utf8"));
  assert.ok(lock.files.includes("go/notes/v2/content.pb.go"));
  assert.ok(lock.files.includes("typescript/notes/v2/content_pb.ts"));
  assert.equal(lock.sha256, (await service.fetchContract(NotesV2.service.typeName)).sha256);

  await writeFile(join(output, "package.json"), JSON.stringify({ type: "module" }));
  await symlink(join(root, "sdks/typescript/node_modules"), join(output, "node_modules"), "dir");
  await writeFile(join(output, "tsconfig.json"), JSON.stringify({
    compilerOptions: { target: "ES2022", module: "NodeNext", moduleResolution: "NodeNext", strict: true, skipLibCheck: true, rootDir: "typescript", outDir: "dist" },
    include: ["typescript/**/*.ts"],
  }));
  await execute(process.execPath, [join(root, "sdks/typescript/node_modules/typescript/bin/tsc"), "-p", join(output, "tsconfig.json")]);
  const generated = await import(pathToFileURL(join(output, "dist/notes/v2/notes_keel.js")).href);
  const fromTypeScript = await service.client(generated.NotesServiceContract).createNote({ content: { text: "downloaded TS client" } });
  assert.equal(fromTypeScript.content.text, "downloaded TS client");

  await writeFile(join(output, "go.mod"), `module example.com/pulled\n\ngo 1.26.0\n\nrequire github.com/marcelsud/keel v0.0.0\nreplace github.com/marcelsud/keel => ${root}\n`);
  await writeFile(join(output, "client_test.go"), `package client_test
import (
  "context"
  "os"
  "testing"
  notes "example.com/pulled/go/notes/v2"
  "github.com/marcelsud/keel/sdk"
)
func TestDownloadedClient(t *testing.T) {
  app, err := sdk.Connect(sdk.Options{Name:"downloaded-go", URL:os.Getenv("KEEL_URL"), Token:os.Getenv("KEEL_TOKEN")})
  if err != nil { t.Fatal(err) }; defer app.Stop()
  note, err := notes.NewNotesServiceClient(app).CreateNote(context.Background(), &notes.CreateNoteRequest{Content:&notes.Content{Text:"downloaded Go client"}})
  if err != nil { t.Fatal(err) }
  if note.GetId() == "" || note.GetContent().GetText() != "downloaded Go client" { t.Fatal(note) }
}
`);
  await execute("go", ["test", "-mod=mod", "./..."], { cwd: output, env: environment });

  lock.sha256 = "0".repeat(64);
  lock.files.push("obsolete_generated.ts");
  await writeFile(join(output, "obsolete_generated.ts"), "// Former generated file\n");
  await writeFile(lockPath, JSON.stringify(lock));
  await assert.rejects(execute(binary, args, { env: environment }), (error) => error.stderr.includes("--update"));
  await execute(binary, [...args, "--update"], { env: environment });
  assert.equal(JSON.parse(await readFile(lockPath, "utf8")).sha256, (await service.fetchContract(NotesV2.service.typeName)).sha256);
  await assert.rejects(readFile(join(output, "obsolete_generated.ts")), { code: "ENOENT" });
});
