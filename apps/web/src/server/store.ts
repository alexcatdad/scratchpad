import { chmodSync, closeSync, mkdirSync, openSync, unlinkSync } from "node:fs";
import { dirname, isAbsolute, resolve } from "node:path";
import Database from "better-sqlite3";
import { and, eq } from "drizzle-orm";
import { drizzle } from "drizzle-orm/better-sqlite3";
import {
  integer,
  primaryKey,
  sqliteTable,
  text,
} from "drizzle-orm/sqlite-core";
import {
  type Actor,
  ApiError,
  type Entity,
  id,
  type JsonObject,
  now,
} from "./domain";

export const entities = sqliteTable(
  "entities",
  {
    kind: text("kind").notNull(),
    id: text("id").notNull(),
    data: text("data", { mode: "json" }).$type<Entity>().notNull(),
    version: integer("version").notNull(),
    createdAt: text("created_at").notNull(),
  },
  (table) => [primaryKey({ columns: [table.kind, table.id] })],
);

export class Store {
  readonly sqlite: Database.Database;
  readonly db: ReturnType<typeof drizzle>;
  constructor(path: string) {
    if (path !== ":memory:")
      mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
    this.sqlite = new Database(path);
    this.sqlite.pragma("journal_mode = WAL");
    this.sqlite.pragma("foreign_keys = ON");
    this.sqlite.pragma("busy_timeout = 5000");
    this.sqlite.exec(`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS entities(kind TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,version INTEGER NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(kind,id));
      CREATE TABLE IF NOT EXISTS identities(identity TEXT PRIMARY KEY,project_id TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS retries(scope TEXT NOT NULL,key TEXT NOT NULL,request TEXT NOT NULL,response TEXT NOT NULL,PRIMARY KEY(scope,key));
      CREATE VIRTUAL TABLE IF NOT EXISTS record_search USING fts5(record_id UNINDEXED,title,content);
      INSERT OR IGNORE INTO schema_migrations VALUES(1,strftime('%Y-%m-%dT%H:%M:%fZ','now'));`);
    this.db = drizzle(this.sqlite);
  }
  get(kind: string, id: string): Entity | undefined {
    return this.db
      .select()
      .from(entities)
      .where(and(eq(entities.kind, kind), eq(entities.id, id)))
      .get()?.data;
  }
  list(kind: string): Entity[] {
    return this.db
      .select()
      .from(entities)
      .where(eq(entities.kind, kind))
      .all()
      .map((row) => row.data);
  }
  insert(kind: string, value: JsonObject & { id: string }): Entity {
    const entity = {
      ...value,
      createdAt: typeof value.createdAt === "string" ? value.createdAt : now(),
      version: typeof value.version === "number" ? value.version : 1,
    } as Entity;
    this.db
      .insert(entities)
      .values({
        kind,
        id: entity.id,
        data: entity,
        version: entity.version,
        createdAt: entity.createdAt,
      })
      .run();
    return entity;
  }
  update(kind: string, entity: Entity, expected?: number): Entity {
    const previous = this.get(kind, entity.id);
    if (!previous)
      throw new ApiError(404, "NOT_FOUND", `${kind} does not exist.`);
    if (expected !== undefined && previous.version !== expected)
      throw new ApiError(
        409,
        "CONFLICT",
        "This item changed since it was read.",
        { currentVersion: previous.version },
      );
    const next = { ...entity, version: previous.version + 1, updatedAt: now() };
    const result = this.db
      .update(entities)
      .set({ data: next, version: next.version })
      .where(
        and(
          eq(entities.kind, kind),
          eq(entities.id, entity.id),
          eq(entities.version, previous.version),
        ),
      )
      .run();
    if (result.changes !== 1)
      throw new ApiError(
        409,
        "CONFLICT",
        "Concurrent update. Read the item again.",
      );
    return next;
  }
  remove(kind: string, id: string): void {
    this.db
      .delete(entities)
      .where(and(eq(entities.kind, kind), eq(entities.id, id)))
      .run();
  }
  audit(
    action: string,
    entityType: string,
    entityId: string,
    actor: Actor,
    previous?: unknown,
    next?: unknown,
  ): void {
    this.insert("audit", {
      id: id("audit"),
      action,
      entityType,
      entityId,
      actor,
      previous,
      next,
    });
  }
  atomic<T>(fn: () => T): T {
    return this.sqlite.transaction(fn).immediate();
  }
  async backup(destination: string): Promise<void> {
    if (!isAbsolute(destination))
      throw new ApiError(
        400,
        "VALIDATION_FAILED",
        "Backup destination must be an absolute path.",
      );
    if (resolve(destination) === resolve(this.sqlite.name))
      throw new ApiError(
        409,
        "CONFLICT",
        "Backup destination must differ from the live database.",
      );
    mkdirSync(dirname(destination), { recursive: true, mode: 0o700 });
    const file = openSync(destination, "wx", 0o600);
    closeSync(file);
    try {
      await this.sqlite.backup(destination);
      chmodSync(destination, 0o600);
    } catch (error) {
      unlinkSync(destination);
      throw error;
    }
  }
  close(): void {
    this.sqlite.close();
  }
}
