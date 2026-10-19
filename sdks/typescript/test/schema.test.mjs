import assert from "node:assert/strict";
import { test } from "node:test";
import { schema, ValidationError } from "../dist/index.js";

test("schema primitives validate without coercion", () => {
  for (const [validator, valid, invalid] of [
    [schema.string(), "Ada", [42, null, undefined]],
    [schema.boolean(), false, [0, "true"]],
    [schema.number(), 1.5, ["1.5", NaN, Infinity, -Infinity]],
    [schema.integer(), 42, [1.5, Number.MAX_SAFE_INTEGER + 1, NaN]],
    [schema.null(), null, [undefined, false]],
    [schema.literal("ready"), "ready", ["other", 1]],
    [schema.enum(["alpha", "stable"]), "stable", ["other", 1]],
  ]) {
    assert.equal(validator.parse(valid), valid);
    for (const value of invalid) assert.throws(() => validator.parse(value), ValidationError);
  }
  assert.equal(schema.unknown().parse(undefined), undefined);
  assert.equal(schema.any().parse(null), null);
});

test("nested objects strip extra fields and report field paths", () => {
  const request = schema.object({
    users: schema.object({ name: schema.string(), age: schema.integer().optional() }).array(),
    labels: schema.record(schema.boolean()),
  });
  assert.deepEqual(request.parse({ users: [{ name: "Ada", extra: true }], labels: { active: true }, extra: 42 }), {
    users: [{ name: "Ada" }], labels: { active: true },
  });
  for (const invalid of [null, [], new Date(), Object.create({ users: [] })]) {
    assert.throws(() => request.parse(invalid), /value must be an object/);
  }
  assert.throws(() => request.parse({ users: [{}], labels: {} }), /value.users\[0\].name is required/);
  assert.throws(() => request.parse({ users: [{ name: 42 }], labels: {} }), /value.users\[0\].name must be a string/);
  assert.throws(() => request.parse({ users: [], labels: { active: "true" } }), /value.labels.active must be a boolean/);
  assert.throws(() => schema.object({ value: schema.unknown() }).parse({}), /value.value is required/);
  assert.deepEqual(schema.object({ name: schema.string() }).parse(Object.assign(Object.create(null), { name: "Ada" })), { name: "Ada" });
});

test("optional, nullable, arrays, and unions compose", () => {
  const request = schema.object({
    nickname: schema.string().nullable().optional(),
    alias: schema.string().optional().nullable(),
    scores: schema.optional(schema.array(schema.number())),
  });
  assert.deepEqual(request.parse({}), {});
  assert.deepEqual(request.parse({ nickname: null, alias: null, scores: [1, 2.5] }), {
    nickname: null, alias: null, scores: [1, 2.5],
  });
  assert.deepEqual(request.parse({ nickname: undefined }), { nickname: undefined });
  assert.throws(() => schema.string().nullable().parse(undefined), ValidationError);
  assert.throws(() => schema.string().array().parse("Ada"), /must be an array/);
  assert.throws(() => request.parse({ scores: ["1"] }), /value.scores\[0\]/);
  const choice = schema.union(schema.literal("auto"), schema.integer());
  assert.equal(choice.parse("auto"), "auto");
  assert.equal(choice.parse(42), 42);
  assert.throws(() => choice.parse(false), ValidationError);
});

test("object and record parsers preserve safe own properties for special keys", () => {
  const input = JSON.parse('{"__proto__":"safe","constructor":"value"}');
  const validators = [
    schema.record(schema.string()),
    schema.object(Object.fromEntries([["__proto__", schema.string()], ["constructor", schema.string()]])),
  ];
  for (const validator of validators) {
    const output = validator.parse(input);
    assert.equal(Object.getPrototypeOf(output), Object.prototype);
    assert.equal(Object.hasOwn(output, "__proto__"), true);
    assert.deepEqual(output, input);
  }
});
