export class ValidationError extends Error {}

export interface Validator<T = unknown, Optional extends boolean = boolean> {
  readonly isOptional: Optional;
  parse(value: unknown, path?: string): T;
  array(): Validator<T[], false>;
  nullable(): Validator<T | null, Optional>;
  optional(): Validator<T | undefined, true>;
}

export type Infer<V extends Validator> = V extends Validator<infer T> ? T : never;

type Shape = Record<string, Validator>;
type OptionalKeys<S extends Shape> = {
  [K in keyof S]: S[K]["isOptional"] extends true ? K : never;
}[keyof S];
type ObjectValue<S extends Shape> = {
  [K in Exclude<keyof S, OptionalKeys<S>>]: Infer<S[K]>;
} & {
  [K in OptionalKeys<S>]?: Infer<S[K]>;
};

function validator<T, Optional extends boolean>(
  parse: (value: unknown, path: string) => T,
  isOptional: Optional,
): Validator<T, Optional> {
  return {
    isOptional,
    parse: (value, path = "value") => parse(value, path),
    array() {
      return validator((value, path) => {
        if (!Array.isArray(value)) throw new ValidationError(`${path} must be an array`);
        return value.map((item, index) => parse(item, `${path}[${index}]`));
      }, false);
    },
    nullable() {
      return validator((value, path) => value === null ? null : parse(value, path), isOptional);
    },
    optional() {
      return validator((value, path) => value === undefined ? undefined : parse(value, path), true);
    },
  };
}

function scalar<T>(type: string, accepts: (value: unknown) => value is T): Validator<T, false> {
  return validator((value, path) => {
    if (!accepts(value)) throw new ValidationError(`${path} must be ${type}`);
    return value;
  }, false);
}

function object(value: unknown, path: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null ||
      ![Object.prototype, null].includes(Object.getPrototypeOf(value))) {
    throw new ValidationError(`${path} must be an object`);
  }
  return value as Record<string, unknown>;
}

const unknown = (): Validator<unknown, false> => validator((value) => value, false);

export const schema = {
  string: () => scalar("a string", (value): value is string => typeof value === "string"),
  boolean: () => scalar("a boolean", (value): value is boolean => typeof value === "boolean"),
  number: () => scalar("a finite number", (value): value is number => typeof value === "number" && Number.isFinite(value)),
  integer: () => scalar("a safe integer", (value): value is number => typeof value === "number" && Number.isSafeInteger(value)),
  null: () => scalar("null", (value): value is null => value === null),
  unknown,
  any: unknown,
  object<S extends Shape>(shape: S): Validator<ObjectValue<S>, false> {
    return validator((value, path) => {
      const input = object(value, path);
      return Object.fromEntries(Object.entries(shape).flatMap(([key, field]) => {
        if (!Object.hasOwn(input, key)) {
          if (field.isOptional) return [];
          throw new ValidationError(`${path}.${key} is required`);
        }
        return [[key, field.parse(input[key], `${path}.${key}`)]];
      })) as ObjectValue<S>;
    }, false);
  },
  array<T>(item: Validator<T>): Validator<T[], false> {
    return item.array();
  },
  optional<T>(item: Validator<T>): Validator<T | undefined, true> {
    return item.optional();
  },
  record<T>(item: Validator<T>): Validator<Record<string, T>, false> {
    return validator((value, path) => Object.fromEntries(
      Object.entries(object(value, path)).map(([key, entry]) => [key, item.parse(entry, `${path}.${key}`)]),
    ), false);
  },
  literal<const T extends string | number | boolean | null>(expected: T): Validator<T, false> {
    return scalar(JSON.stringify(expected), (value): value is T => value === expected);
  },
  enum<const Values extends readonly [string, ...string[]]>(values: Values): Validator<Values[number], false> {
    return scalar(`one of ${values.join(", ")}`, (value): value is Values[number] => typeof value === "string" && values.includes(value));
  },
  union<const V extends readonly [Validator, ...Validator[]]>(...members: V): Validator<Infer<V[number]>, false> {
    return validator((value, path) => {
      let firstError;
      for (const member of members) {
        try { return member.parse(value, path) as Infer<V[number]>; }
        catch (error) { firstError ??= error; }
      }
      throw firstError;
    }, false);
  },
};
