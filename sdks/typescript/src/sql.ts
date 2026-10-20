import { ServiceError } from "@nats-io/services";

type Transport = <T>(subject: string, input: unknown) => Promise<T>;
export type SQLParameter = string | number | boolean | null;
export type SQLResult<T extends object = Record<string, unknown>> = {
  rows: T[];
  rowCount: number;
};
type Handle = { id: string; subject: string; expiresAt: number };

function validateParams(params: SQLParameter[]): void {
  for (const param of params) {
    if (
      typeof param === "number" &&
      (!Number.isFinite(param) ||
        (Number.isInteger(param) && !Number.isSafeInteger(param)))
    )
      throw new Error(
        "SQL numbers must be finite; pass large integers as strings and CAST explicitly",
      );
  }
}

export class SQLCommitUnknownError extends Error {
  constructor(cause: unknown) {
    super(
      "SQL commit outcome unknown; reconcile using the operation's idempotency key",
      { cause },
    );
    this.name = "SQLCommitUnknownError";
  }
}

export interface SQLTransaction {
  query<T extends object = Record<string, unknown>>(
    statement: string,
    params?: SQLParameter[],
  ): Promise<SQLResult<T>>;
}

class Transaction implements SQLTransaction {
  private closed = false;
  private busy = false;
  constructor(
    private request: Transport,
    private handle: Handle,
  ) {}

  async query<T extends object = Record<string, unknown>>(
    statement: string,
    params: SQLParameter[] = [],
  ): Promise<SQLResult<T>> {
    if (this.closed) throw new Error("SQL transaction is closed");
    if (this.busy)
      throw new Error(
        "Await each SQL transaction query before starting the next",
      );
    this.busy = true;
    try {
      validateParams(params);
      return await this.request(this.handle.subject + ".query", {
        statement,
        params,
        transaction: this.handle.id,
      });
    } catch (error) {
      this.closed = true;
      throw error;
    } finally {
      this.busy = false;
    }
  }

  async finish(commit: boolean): Promise<void> {
    if (commit && (this.closed || this.busy))
      throw new Error("SQL transaction failed or has an unawaited query");
    this.closed = true;
    await this.request(
      this.handle.subject + (commit ? ".commit" : ".rollback"),
      { transaction: this.handle.id },
    );
  }
}

export class SQL {
  private subject: string;
  constructor(
    scope: string,
    private request: Transport,
  ) {
    this.subject = `keel.v1.sql.${scope}`;
  }

  query<T extends object = Record<string, unknown>>(
    statement: string,
    params: SQLParameter[] = [],
  ): Promise<SQLResult<T>> {
    validateParams(params);
    return this.request(this.subject + ".query", { statement, params });
  }

  async transaction<T>(
    callback: (tx: SQLTransaction) => T | Promise<T>,
  ): Promise<T> {
    const handle = await this.request<Handle>(this.subject + ".begin", {});
    if (
      !handle.id ||
      !handle.subject.startsWith(this.subject + ".tx.") ||
      !/^[a-f0-9]+$/.test(handle.subject.slice((this.subject + ".tx.").length))
    )
      throw new Error("Invalid SQL transaction handle");
    const tx = new Transaction(this.request, handle);
    let committed = false;
    try {
      const result = await callback(tx);
      try {
        await tx.finish(true);
      } catch (error) {
        if (error instanceof ServiceError && [409, 410].includes(error.code))
          throw error;
        if (
          error instanceof Error &&
          error.message.startsWith("SQL transaction failed")
        )
          throw error;
        throw new SQLCommitUnknownError(error);
      }
      committed = true;
      return result;
    } finally {
      if (!committed) await tx.finish(false).catch(() => {});
    }
  }
}
