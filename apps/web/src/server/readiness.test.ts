import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import Database from "better-sqlite3";
import { afterEach, describe, expect, it } from "vitest";
import { createApi } from "./api";
import { Store } from "./store";

const cleanups: (() => void | Promise<void>)[] = [];
afterEach(async () => {
  for (const cleanup of cleanups.splice(0).reverse()) await cleanup();
});
function fixture() {
  const api = createApi({
    databasePath: ":memory:",
    origin: "http://localhost:3000",
  });
  cleanups.push(async () => {
    if (api.store.sqlite.open) await api.close();
  });
  return api;
}
async function ready(api: ReturnType<typeof createApi>) {
  const response = await api.handleRequest(
    new Request("http://localhost:3000/ready"),
  );
  return { status: response.status, body: await response.json() };
}

describe("SQLite readiness", () => {
  it("reports a bootstrapped database ready without owner enrollment or AI", async () => {
    expect(await ready(await fixture())).toEqual({
      status: 200,
      body: { status: "ready", database: "sqlite" },
    });
  });
  it.each([
    "DELETE FROM schema_migrations",
    "INSERT INTO schema_migrations VALUES(2,'future')",
    "DROP TABLE schema_migrations",
    "ALTER TABLE schema_migrations RENAME COLUMN applied_at TO unreadable_applied_at",
    "DROP TABLE entities",
    "DROP TABLE identities",
    "DROP TABLE retries",
    "DROP TABLE record_search",
    "ALTER TABLE retries RENAME COLUMN response TO unreadable_response",
  ])("fails readiness when persistence is invalid: %s", async (sql) => {
    const api = await fixture();
    api.store.sqlite.exec(sql);
    const result = await ready(api);
    expect(result.status).toBe(503);
    expect(result.body.error.code).toBe("DATABASE_NOT_READY");
  });
  it("fails readiness after losing its persistence connection", async () => {
    const api = await fixture();
    await api.close();
    expect((await ready(api)).status).toBe(503);
  });
  it.each([
    "INSERT INTO schema_migrations VALUES(2,'future')",
    "DELETE FROM schema_migrations",
    "DROP TABLE schema_migrations",
    "DROP TABLE entities",
    "ALTER TABLE entities RENAME COLUMN kind TO unreadable_kind",
    "ALTER TABLE retries RENAME COLUMN response TO unreadable_response",
  ])(
    "refuses an invalid existing database without modifying it: %s",
    async (sql) => {
      const directory = mkdtempSync(join(tmpdir(), "scratchpad-readiness-"));
      cleanups.push(() => rmSync(directory, { recursive: true, force: true }));
      const path = join(directory, "memory.sqlite");
      const initial = new Store(path);
      initial.sqlite.pragma("journal_mode = DELETE");
      initial.sqlite.exec(sql);
      await initial.close();
      const before = readFileSync(path);
      expect(() => new Store(path)).toThrow(
        "SQLite persistence or schema migrations are unavailable or unsupported.",
      );
      expect(readFileSync(path)).toEqual(before);
      const inspection = new Database(path, { readonly: true });
      try {
        expect(inspection.pragma("journal_mode", { simple: true })).toBe(
          "delete",
        );
      } finally {
        await inspection.close();
      }
    },
  );
  it("does not bootstrap an unrelated existing database", async () => {
    const directory = mkdtempSync(join(tmpdir(), "scratchpad-unrelated-"));
    cleanups.push(() => rmSync(directory, { recursive: true, force: true }));
    const path = join(directory, "unrelated.sqlite");
    const original = new Database(path);
    original.exec(
      "CREATE TABLE unrelated(value TEXT); INSERT INTO unrelated VALUES('preserve existing data')",
    );
    await original.close();
    const before = readFileSync(path);
    expect(() => new Store(path)).toThrow(
      "SQLite persistence or schema migrations are unavailable or unsupported.",
    );
    expect(readFileSync(path)).toEqual(before);
  });
});
