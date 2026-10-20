import assert from "node:assert/strict";
import { test } from "node:test";
import { createHttpRouter } from "../dist/http.js";
import { schema, ValidationError } from "../dist/index.js";

const request = (path, init) => new Request(`http://localhost${path}`, init);

test("HTTP paths, methods, validation and response mapping", async () => {
  const router = createHttpRouter();
  router.route("GET", "/notes/:id", ({ params, query }) => ({ params, query }));
  router.route("GET", "/notes/new", () => "literal");
  router.route("POST", "/notes", {
    body: schema.object({ title: schema.string() }),
    action: async (input) => input,
    output: (data) => ({ data }), status: 201,
  });
  assert.equal(await (await router.fetch(request("/notes/new"))).json(), "literal");
  assert.deepEqual(await (await router.fetch(request("/notes/a%2Fb?tag=a&tag=b&__proto__=safe"))).json(), {
    params: { id: "a/b" }, query: { tag: ["a", "b"], ["__proto__"]: "safe" },
  });
  assert.equal((await router.fetch(request("/notes/%ZZ"))).status, 400);
  const created = await router.fetch(request("/notes", { method: "POST", body: '{"title":"hello","extra":true}' }));
  assert.equal(created.status, 201);
  assert.deepEqual(await created.json(), { data: { title: "hello" } });
  for (const body of ["{", '{"title":1}']) {
    assert.equal((await router.fetch(request("/notes", { method: "POST", body }))).status, 400);
  }
  for (const [method, path, status, allow] of [
    ["GET", "/missing", 404], ["DELETE", "/notes/new", 405, "GET, HEAD, OPTIONS"],
    ["OPTIONS", "/notes/new", 204, "GET, HEAD, OPTIONS"], ["HEAD", "/notes/new", 200],
  ]) {
    const response = await router.fetch(request(path, { method }));
    assert.equal(response.status, status);
    if (allow) assert.equal(response.headers.get("allow"), allow);
    if (method === "HEAD") assert.equal(await response.text(), "");
  }
  router.route("HEAD", "/notes/new", () => new Response(null, { status: 202 }));
  assert.equal((await router.fetch(request("/notes/new", { method: "HEAD" }))).status, 202);
  for (const status of [204, 205, 304]) {
    router.route("DELETE", `/empty/${status}`, { handler: () => 1n, status });
    assert.equal(await (await router.fetch(request(`/empty/${status}`, { method: "DELETE" }))).text(), "");
  }
  router.route("GET", "/raw", () => new Response("raw", { headers: { "X-Test": "ok" } }));
  assert.equal((await router.fetch(request("/raw"))).headers.get("X-Test"), "ok");
});

test("invalid declarations and parameter shapes fail before serving", () => {
  const router = createHttpRouter();
  const handler = () => null;
  for (const path of ["relative", "/a//b", "/a/../b", "/café", "/a?b", "/a/*", "/:id/:id"]) {
    assert.throws(() => router.route("GET", path, handler));
  }
  router.route("GET", "/:id", handler);
  assert.throws(() => router.route("GET", "/:other", handler), /Duplicate/);
  assert.throws(() => router.route("TRACE", "/", handler), /method/);
  assert.throws(() => router.route("GET", "/both", { handler, action: handler }), /exactly one/);
  assert.throws(() => router.route("GET", "/invalid", { handler, status: 99 }), /status/);
});

test("validation errors from output mapping remain server errors", async (t) => {
  const router = createHttpRouter();
  t.mock.method(console, "error", () => {});
  router.route("GET", "/", { handler: () => true, output() { throw new ValidationError("private"); } });
  const response = await router.fetch(request("/"));
  assert.equal(response.status, 500);
  assert.deepEqual(await response.json(), { error: "Internal server error" });
  router.route("GET", "/invalid-json", () => ({ invalid: undefined }));
  assert.equal((await router.fetch(request("/invalid-json"))).status, 500);
});
