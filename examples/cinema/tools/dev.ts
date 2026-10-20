import { spawn, type ChildProcess } from "node:child_process";
import {
  access,
  chmod,
  copyFile,
  mkdir,
  realpath,
  readFile,
} from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { once } from "node:events";

export const directory = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repository = resolve(directory, "../..");
const binaries = join(directory, ".keel/bin");

async function command(args: string[], cwd = directory): Promise<void> {
  const child = spawn(args[0]!, args.slice(1), {
    cwd,
    stdio: "inherit",
    env: { ...process.env, PATH: `${binaries}:${process.env.PATH ?? ""}` },
  });
  const [code, signal] = await once(child, "exit");
  if (code !== 0)
    throw new Error(`${args.join(" ")} failed (${code ?? signal})`);
}

export async function build(): Promise<void> {
  await mkdir(binaries, { recursive: true });
  if (!Bun.which("buf")) {
    try {
      await access(join(binaries, "buf"));
    } catch {
      const installed = Bun.which("keel");
      if (!installed)
        throw new Error(
          "Install the Keel CLI first so setup can reuse its bundled contract compiler.",
        );
      await copyFile(
        join(dirname(await realpath(installed)), "buf"),
        join(binaries, "buf"),
      );
      await chmod(join(binaries, "buf"), 0o755);
    }
  }
  if (process.env.KEEL_SQL_ENGINE === "dqlite") {
    await command(
      ["sh", "scripts/build-cluster.sh", join(binaries, "keel")],
      repository,
    );
  } else
    await command(
      [
        "go",
        "build",
        "-tags=keel_libsql",
        "-buildvcs=false",
        "-o",
        join(binaries, "keel"),
        "./cmd/keel",
      ],
      repository,
    );
  await command(
    [
      "go",
      "build",
      "-o",
      join(binaries, "protoc-gen-go"),
      "google.golang.org/protobuf/cmd/protoc-gen-go",
    ],
    repository,
  );
  await command([join(binaries, "keel"), "generate"]);
  for (const service of ["booking", "checkout"]) {
    await command(
      [
        "go",
        "build",
        "-buildvcs=false",
        "-o",
        join(binaries, service),
        `./examples/cinema/services/${service}/cmd`,
      ],
      repository,
    );
  }
}

export type RunningService = {
  name: string;
  child: ChildProcess;
  exited: Promise<unknown>;
  output: string;
};
export async function stopServices(services: RunningService[]): Promise<void> {
  for (const { child } of services)
    if (child.exitCode === null && child.signalCode === null)
      child.kill("SIGTERM");
  const timer = setTimeout(() => {
    for (const { child } of services)
      if (child.exitCode === null && child.signalCode === null)
        child.kill("SIGKILL");
  }, 6000);
  try {
    await Promise.all(services.map((service) => service.exited));
  } finally {
    clearTimeout(timer);
  }
}

export async function startServices(
  environment: NodeJS.ProcessEnv = process.env,
  verbose = true,
): Promise<RunningService[]> {
  const services: RunningService[] = [];
  const credentials = await readCredentials(environment);
  try {
    for (const name of [
      "catalog",
      "booking",
      "payments",
      "notifications",
      "checkout",
    ]) {
      const args =
        name === "booking" || name === "checkout"
          ? [join(binaries, name)]
          : ["bun", "run", `services/${name}/main.ts`];
      const child = spawn(args[0]!, args.slice(1), {
        cwd: directory,
        env: {
          ...environment,
          KEEL_USER: `cinema-${name}`,
          KEEL_TOKEN: credentials.services[`cinema-${name}`],
        },
        stdio: ["ignore", "pipe", "pipe"],
      });
      const service: RunningService = {
        name,
        child,
        exited: once(child, "exit"),
        output: "",
      };
      services.push(service);
      await new Promise<void>((resolve, reject) => {
        const timer = setTimeout(
          () => reject(new Error(`${name} did not start: ${service.output}`)),
          10_000,
        );
        const collect = (chunk: Buffer) => {
          const text = chunk.toString();
          service.output += text;
          if (verbose) process.stdout.write(text);
          if (service.output.includes(`cinema-${name} ready`)) {
            clearTimeout(timer);
            resolve();
          }
        };
        child.stdout!.on("data", collect);
        child.stderr!.on("data", collect);
        child.once("error", (error) => {
          clearTimeout(timer);
          reject(error);
        });
        child.once("exit", () => {
          clearTimeout(timer);
          reject(new Error(`${name} exited: ${service.output}`));
        });
      });
    }
    return services;
  } catch (error) {
    await stopServices(services);
    throw error;
  }
}

export type Credentials = { admin: string; services: Record<string, string> };
export async function readCredentials(
  environment: NodeJS.ProcessEnv = process.env,
): Promise<Credentials> {
  const path =
    environment.KEEL_CREDENTIALS_FILE ??
    join(directory, ".keel/credentials.json");
  return JSON.parse(await readFile(path, "utf8"));
}

if (import.meta.main) {
  if (process.argv.includes("--setup")) {
    await command(
      ["bun", "install", "--frozen-lockfile", "--ignore-scripts"],
      join(repository, "sdks/typescript"),
    );
    await command(["bun", "run", "build"], join(repository, "sdks/typescript"));
    await command(["bun", "install", "--ignore-scripts"]);
    await build();
    console.log(
      "Cinema ready. Run bun run dev, then bun run demo in another terminal.",
    );
  } else if (process.argv.includes("--build")) {
    await build();
  } else {
    await build();
    const services = await startServices();
    console.log(
      "cinema ready: catalog (TS/JSON), booking (Go/protobuf), payments (TS/protobuf), checkout (Go/JSON), notifications (TS/JSON jobs)",
    );
    let stopping = false;
    const stop = async () => {
      if (stopping) return;
      stopping = true;
      await stopServices(services);
    };
    for (const signal of ["SIGINT", "SIGTERM"] as const)
      process.once(signal, () => {
        void stop();
      });
    for (const service of services)
      void service.exited.then(() => {
        if (!stopping) {
          console.error(`${service.name} exited unexpectedly`);
          process.exitCode = 1;
          void stop();
        }
      });
  }
}
