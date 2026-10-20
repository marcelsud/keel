import {
  createKeel,
  ValidationError,
  type HandlerContext,
} from "../../sdks/typescript/dist/index.js";
import { NotesServiceContract as NotesV1 } from "../../sdks/typescript/dist/gen/notes/v1/notes_keel.js";
import { NotesServiceContract as NotesV2 } from "../../sdks/typescript/dist/gen/notes/v2/notes_keel.js";

const app = await createKeel({
  name: "notes-typescript",
  url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

if (process.argv.includes("--client")) {
  console.info(
    await app
      .client(NotesV1)
      .createNote({ body: "TypeScript client using v1" }),
  );
  console.info(
    await app
      .client(NotesV2)
      .createNote({ content: { text: "TypeScript client using v2" } }),
  );
  await app.stop();
} else {
  async function create(
    ctx: HandlerContext,
    content: { text: string; title?: string } | undefined,
  ) {
    if (!content?.text.trim())
      throw new ValidationError("content.text must not be empty");
    const document: Record<string, unknown> = { text: content.text };
    if (content.title !== undefined) document.title = content.title;
    const id = await ctx.collection.insert("notes", document);
    return { id, content };
  }

  app.service(
    NotesV1,
    {
      async createNote(ctx, input) {
        const note = await create(ctx, { text: input.body });
        return { id: note.id, body: note.content.text };
      },
    },
    {
      state: process.env.KEEL_RETIRE_V1 === "1" ? "retired" : "deprecated",
      replacement: NotesV2.service.typeName,
    },
  );

  app.service(NotesV2, {
    async createNote(ctx, input) {
      return create(ctx, input.content);
    },
  });

  await app.start();
  console.info("notes-typescript ready");
}
