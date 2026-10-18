# @marcelsud/keel.ts

Use Keel from TypeScript to write services, call typed APIs, store data, publish events, and run background jobs.

## Install

Requires Node.js 22 or newer.

```sh
npm install @marcelsud/keel.ts
```

You need a running Keel environment. Follow the [local walkthrough](https://github.com/marcelsud/keel#try-it-locally) to try services and clients in both Go and TypeScript.

## Connect and store data

```ts
import { createKeel } from "@marcelsud/keel.ts";

const app = await createKeel({
  name: "notes",
  url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

try {
  const id = await app.collection.insert("notes", { body: "Hello" });
  console.log(await app.collection.get("notes", id));
} finally {
  await app.stop();
}
```

## Call a typed service

Install the [Keel CLI](https://github.com/marcelsud/keel#install-the-cli). Its release bundle includes the generators; TypeScript generation uses your Node.js runtime.

If you own the service, put its contracts under `proto/` and run:

```sh
keel generate
```

If you consume a service from a neighboring checkout, run in your consumer project:

```sh
keel use ../notes
```

Both commands generate into `generated/` without a release or running service. `use` also saves the local dependency in `keel.json`. Run `keel generate` again after changes, or use [keel dev](https://github.com/marcelsud/keel#develop-before-releasing) to regenerate and restart your service automatically.

Import the generated contract to get completion and type checking for its methods, requests, and responses. For `proto/notes/v2/notes.proto`, the generated source is `generated/notes/v2/notes_keel.ts`; use a `.js` import for your compiled application:

```ts
import { createKeel } from "@marcelsud/keel.ts";
import { NotesServiceContract } from "./generated/notes/v2/notes_keel.js";

const app = await createKeel({
  name: "notes-client",
  url: process.env.KEEL_URL,
  token: process.env.KEEL_TOKEN,
});

try {
  const notes = app.client(NotesServiceContract);
  const note = await notes.createNote({
    content: { text: "Hello from TypeScript" },
  });
  console.log(note.id);
} finally {
  await app.stop();
}
```

Contracts belong to your project. See the [complete service example](https://github.com/marcelsud/keel/blob/main/examples/contracts/typescript.ts) for service registration, validation, and version migration.

## Publish this SDK

From a checkout of the Keel repository, install the development dependencies using the repository's setup instructions. Publishing also requires Go 1.26+ and Node.js 24+ to run the cross-language examples in the tests.

Run these commands from `sdks/typescript`:

```sh
npm login
npm publish --dry-run
npm publish
```

Publishing runs the tests and builds the package. Packing with `npm pack` also builds it. Consumers receive JavaScript, TypeScript declarations, this README, and the MIT license.

For GitHub Actions, configure an [npm trusted publisher](https://docs.npmjs.com/trusted-publishers/) for this package:

- GitHub owner: `marcelsud`
- Repository: `keel`
- Workflow filename: `publish-npm.yml`
- Allow direct publishing with `npm publish`.

For a new package, make the first publish manually, then configure its trusted publisher. The workflow uses OIDC authentication.

Set the SDK version in `package.json`, then push a matching tag such as `keel.ts-v0.1.0`. You can also run **Publish TypeScript SDK** manually from GitHub Actions. Stable versions publish to `latest`; prerelease versions publish to `next` in the workflow. For a manual prerelease, use `npm publish --tag next`.

## License

[MIT](https://github.com/marcelsud/keel/blob/main/LICENSE)
