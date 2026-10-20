import { type Keel, schema } from "../src/index.js";
import { node } from "../src/http/node.js";

declare const app: Keel;
const create = app.action({ name: "create", args: schema.object({ title: schema.string() }), handler: (_ctx, input) => ({ id: input.title }) });
app.route("POST", "/notes/:id", {
  body: schema.object({ data: schema.object({ title: schema.string() }) }),
  params: schema.object({ id: schema.string() }),
  query: schema.object({ tag: schema.string().array().optional() }),
  action: create,
  input: ({ body, params, query }) => {
    const id: string = params.id;
    const tags: string[] | undefined = query.tag;
    void [id, tags];
    return body.data;
  },
  output: (result) => {
    const id: string = result.id;
    // @ts-expect-error action results retain their inferred type.
    const invalid: number = result.id;
    return { id, invalid };
  },
});
app.route("GET", "/health", ({ request }) => ({ ok: request.method === "GET" }));
// @ts-expect-error Unsupported HTTP method.
app.route("TRACE", "/", () => null);
// @ts-expect-error Input must match the action's argument type.
app.route("POST", "/invalid", { action: create, input: () => ({ title: 42 }) });
void app.start({ signals: false, http: node({ port: 0 }) });
