import { createKeel } from "../sdks/typescript/dist/index.js";

const app = await createKeel({ name: "notes-typescript", url: process.env.KEEL_URL, token: process.env.KEEL_TOKEN });

const createNote = app.action({
  name: "createNote",
  args: { parse(input) {
    if (!input || typeof input.body !== "string" || !input.body.trim()) throw new Error("body must be a nonempty string");
    return { body: input.body };
  } },
  async handler(ctx, input) {
    const id = await ctx.collection.insert("notes", input);
    await ctx.pubsub.publish("note.created", { id });
    return { id, ...input };
  },
});

app.subscription("note.created", async (ctx, event) => {
  await ctx.queue.enqueue("note.notify", event);
}, { name: "notifyOnNoteCreated" });

app.worker("note.notify", (ctx, job) => { ctx.log.info("Note created", job); });

await app.start();
console.info(await createNote({ body: "First note" }));
