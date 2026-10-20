import { createKeel } from "../dist/index.js";

const app = await createKeel({ name: "typescript", url: process.env.KEEL_URL, token: process.env.KEEL_TOKEN });
app.action({ name: "roundtrip", async handler(ctx, input) {
  return { ...await ctx.call("go", "echo", input), language: "typescript" };
} });
app.action({ name: "shared", async handler(ctx) { return { value: await ctx.kv.get("from-go") }; } });
app.worker("typescript", async (ctx) => { await ctx.kv.set("typescript-job", true); });
await app.start();
process.stdout.write("ready\n");
