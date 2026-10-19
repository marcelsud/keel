import { schema, type Infer, type Keel, type Validator } from "../src/index.js";

declare const app: Keel;

const input = schema.object({
  name: schema.string(),
  nickname: schema.string().nullable().optional(),
  alias: schema.string().optional().nullable(),
  role: schema.enum(["reader", "writer"]),
  items: schema.object({ count: schema.integer() }).array(),
  labels: schema.record(schema.boolean()),
  mode: schema.union(schema.literal("auto"), schema.number()),
});

const valid: Infer<typeof input> = {
  name: "Ada", role: "reader", items: [{ count: 1 }], labels: { active: true }, mode: "auto",
};
const nullable: Infer<typeof input> = { ...valid, nickname: null, alias: null };
void nullable;

// @ts-expect-error name is required.
const missing: Infer<typeof input> = { role: "writer", items: [], labels: {}, mode: 1 };
// @ts-expect-error role is a literal union, not an arbitrary string.
const invalidRole: Infer<typeof input> = { ...valid, role: "admin" };
// @ts-expect-error array items must contain a numeric count.
const invalidItem: Infer<typeof input> = { ...valid, items: [{ count: "1" }] };
void [missing, invalidRole, invalidItem];

const greet = app.action({
  name: "greet",
  args: app.schema.object({ name: app.schema.string(), age: app.schema.integer().optional() }),
  handler(_ctx, request) {
    const name: string = request.name;
    const age: number | undefined = request.age;
    void age;
    // @ts-expect-error name is inferred as a string.
    const invalid: number = request.name;
    void invalid;
    return { message: `Hello, ${name}!` };
  },
});

const result: Promise<{ message: string }> = greet({ name: "Ada" });
void result;
// @ts-expect-error action input requires a name.
greet({});
// @ts-expect-error name must be a string.
greet({ name: 42 });

const optional: Validator<string | undefined> = schema.string().optional();
void optional;
// @ts-expect-error unknown still requires its containing key.
const missingUnknown: Infer<ReturnType<typeof withUnknown>> = {};
function withUnknown() { return schema.object({ value: schema.unknown() }); }
void missingUnknown;
