import { createKeel, type Keel } from "@marcelsud/keel.ts";

export const connect = (name: string) =>
  createKeel({
    name,
    url: process.env.KEEL_URL,
    token: process.env.KEEL_TOKEN,
    user: process.env.KEEL_USER,
  });
export function periodic(app: Keel, task: () => Promise<void>): void {
  let running = false;
  const timer = setInterval(async () => {
    if (running) return;
    running = true;
    try {
      await task();
    } catch (error) {
      if (error instanceof Error && error.message === "app is stopped")
        clearInterval(timer);
      else app.log.error("background work will retry", error);
    } finally {
      running = false;
    }
  }, 100);
  timer.unref();
}
