import type { Keel, ServiceImplementation } from "../src/index.js";
import { NotesServiceContract as NotesV1 } from "../src/gen/notes/v1/notes_keel.js";
import { NotesServiceContract as NotesV2 } from "../src/gen/notes/v2/notes_keel.js";

declare const app: Keel;
const notes = app.client(NotesV2);
const result = notes.createNote({ content: { text: "typed", title: "Title" } });
result.then((note) => { const id: string = note.id; return id; });

// @ts-expect-error v2 requests do not have the v1 body field.
notes.createNote({ body: "wrong version" });
// @ts-expect-error text is a string.
notes.createNote({ content: { text: 42 } });
// @ts-expect-error unknown RPC names cannot be called.
notes.removeNote({ id: "1" });
// @ts-expect-error v1 requests do not accept the v2 content field.
app.client(NotesV1).createNote({ content: { text: "wrong version" } });

app.service(NotesV2, {
  async createNote(_ctx, input) {
    const text: string | undefined = input.content?.text;
    return { id: "1", content: { text: text ?? "" } };
  },
});

const invalid: ServiceImplementation<typeof NotesV1.service> = {
  // @ts-expect-error response IDs must be strings.
  createNote: () => ({ id: 42, body: "invalid output" }),
};
void invalid;
