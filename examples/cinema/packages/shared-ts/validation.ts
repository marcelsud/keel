import { ServiceError } from "@marcelsud/keel.ts";

export function fail(code: number, message: string): never {
  throw new ServiceError(code, message);
}
export function identifier(value: string, field: string): void {
  if (
    !/^[a-zA-Z0-9_-]{1,128}$/.test(value) ||
    ["__proto__", "constructor", "prototype"].includes(value)
  )
    fail(
      400,
      `${field} must contain 1–128 letters, numbers, hyphens, or underscores`,
    );
}

export function own<T>(values: Record<string, T>, key: string): T | undefined {
  return Object.hasOwn(values, key) ? values[key] : undefined;
}
