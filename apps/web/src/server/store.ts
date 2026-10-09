import { AsyncLocalStorage } from "node:async_hooks";
import { execFileSync } from "node:child_process";
import {
  chmodSync,
  closeSync,
  constants,
  fstatSync,
  lstatSync,
  mkdirSync,
  openSync,
  type Stats,
  unlinkSync,
} from "node:fs";
import { dirname, isAbsolute, resolve } from "node:path";
import Database from "better-sqlite3";
import { and, eq, type SQL, sql } from "drizzle-orm";
import { drizzle as sqliteDrizzle } from "drizzle-orm/better-sqlite3";
import {
  jsonb,
  integer as pgInteger,
  primaryKey as pgPrimaryKey,
  pgTable,
  text as pgText,
} from "drizzle-orm/pg-core";
import { drizzle as postgresDrizzle } from "drizzle-orm/postgres-js";
import {
  integer as sqliteInteger,
  primaryKey as sqlitePrimaryKey,
  sqliteTable,
  text as sqliteText,
} from "drizzle-orm/sqlite-core";
import postgres from "postgres";
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
    kind: sqliteText("kind").notNull(),
    id: sqliteText("id").notNull(),
    data: sqliteText("data", { mode: "json" }).$type<Entity>().notNull(),
    version: sqliteInteger("version").notNull(),
    createdAt: sqliteText("created_at").notNull(),
  },
  (table) => [sqlitePrimaryKey({ columns: [table.kind, table.id] })],
);
const pgEntities = pgTable(
  "entities",
  {
    kind: pgText("kind").notNull(),
    id: pgText("id").notNull(),
    data: jsonb("data").$type<Entity>().notNull(),
    version: pgInteger("version").notNull(),
    createdAt: pgText("created_at").notNull(),
  },
  (table) => [pgPrimaryKey({ columns: [table.kind, table.id] })],
);
type Sql = ReturnType<typeof postgres>;
type PgDb = ReturnType<typeof postgresDrizzle>;
type PgTx = Parameters<Parameters<PgDb["transaction"]>[0]>[0];

function assertPrivateStorage(path: string, info: Stats, directory: boolean) {
  const uid = process.getuid?.();
  if (
    uid === undefined ||
    info.uid !== uid ||
    (info.mode & 0o077) !== 0 ||
    (directory ? !info.isDirectory() : !info.isFile() || info.nlink !== 1)
  )
    throw new Error(
      "SQLite storage must be owner-only: use an owned dedicated directory (0700) and regular database/WAL/SHM files (0600), without symlinks or hard links. Stop the service and repair permissions before restarting.",
    );
  // macOS ACLs can grant access independently of POSIX group/other mode bits.
  if (
    process.platform === "darwin" &&
    /^\s*\d+: /m.test(
      execFileSync("/bin/ls", ["-lde", "--", path], { encoding: "utf8" }),
    )
  )
    throw new Error(
      "SQLite storage must be owner-only: remove extended ACLs from the selected storage directory and database/WAL/SHM files before restarting.",
    );
}
function privateDirectory(path: string) {
  mkdirSync(path, { recursive: true, mode: 0o700 });
  assertPrivateStorage(path, lstatSync(path), true);
}
function privateSqliteFile(path: string, create: boolean) {
  let fd: number;
  try {
    fd = openSync(
      path,
      constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK,
    );
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    if (!create) return;
    fd = openSync(
      path,
      constants.O_RDWR |
        constants.O_CREAT |
        constants.O_EXCL |
        constants.O_NOFOLLOW |
        constants.O_NONBLOCK,
      0o600,
    );
  }
  try {
    assertPrivateStorage(path, fstatSync(fd), false);
  } finally {
    closeSync(fd);
  }
}
/** Async persistence boundary shared by SQLite and PostgreSQL. */
export class Store {
  readonly backend: "sqlite" | "postgres";
  readonly sqlite: Database.Database;
  readonly pgvector: boolean;
  private pg?: Sql;
  private context = new AsyncLocalStorage<PgTx | true>();
  private orm?: PgDb;
  private ready: Promise<void>;
  private queue: Promise<unknown> = Promise.resolve();
  constructor(
    path: string,
    databaseUrl?: string,
    options: { pgvector?: boolean } = {},
  ) {
    this.pgvector = options.pgvector ?? false;
    if (this.pgvector && !databaseUrl)
      throw new Error("pgvector requires SCRATCHPAD_DATABASE_URL.");
    this.backend = databaseUrl ? "postgres" : "sqlite";
    if (databaseUrl) {
      if (!/^postgres(?:ql)?:\/\//.test(databaseUrl))
        throw new Error("SCRATCHPAD_DATABASE_URL must be a PostgreSQL URL.");
      this.sqlite = undefined as unknown as Database.Database;
      this.pg = postgres(databaseUrl, {
        max: 10,
        connect_timeout: 10,
        idle_timeout: 30,
      });
      this.orm = postgresDrizzle(this.pg);
      this.ready = this.initializePostgres();
      // Keep startup failure observable through readiness without an unhandled rejection.
      void this.ready.catch(() => {});
    } else {
      if (path !== ":memory:") {
        privateDirectory(dirname(path));
        privateSqliteFile(path, true);
        for (const suffix of ["-wal", "-shm", "-journal"])
          privateSqliteFile(path + suffix, false);
      }
      this.sqlite = new Database(path);
      try {
        if (
          this.sqlite
            .prepare(
              "SELECT name FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*' LIMIT 1",
            )
            .get()
        )
          this.checkSqlite();
        this.sqlite.pragma("journal_mode = WAL");
        this.sqlite.pragma("foreign_keys = ON");
        this.sqlite.pragma("busy_timeout = 5000");
        this.sqlite.exec(`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL);
        CREATE TABLE IF NOT EXISTS entities(kind TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,version INTEGER NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(kind,id));
        CREATE TABLE IF NOT EXISTS identities(identity TEXT PRIMARY KEY,project_id TEXT NOT NULL);
        CREATE TABLE IF NOT EXISTS retries(scope TEXT NOT NULL,key TEXT NOT NULL,request TEXT NOT NULL,response TEXT NOT NULL,PRIMARY KEY(scope,key));
        CREATE VIRTUAL TABLE IF NOT EXISTS record_search USING fts5(record_id UNINDEXED,title,content);
        INSERT OR IGNORE INTO schema_migrations VALUES(1,strftime('%Y-%m-%dT%H:%M:%fZ','now'));`);
        this.checkSqlite();
        if (path !== ":memory:") {
          for (const suffix of ["", "-wal", "-shm", "-journal"])
            privateSqliteFile(path + suffix, false);
        }
        this.ready = Promise.resolve();
      } catch (error) {
        this.sqlite.close();
        throw error;
      }
    }
  }
  private async initializePostgres() {
    const pg = this.pg;
    if (!pg) throw new Error("PostgreSQL is not configured.");
    await pg.begin(async (tx) => {
      await tx`SELECT pg_advisory_xact_lock(1397772880)`;
      const tables =
        await tx`SELECT tablename FROM pg_tables WHERE schemaname=current_schema()`;
      if (tables.length) {
        await this.checkPostgres(tx as unknown as Sql);
        if (this.pgvector) await this.initializeVectors(tx as unknown as Sql);
        return;
      }
      await tx.unsafe(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL);
      CREATE TABLE entities(kind TEXT NOT NULL,id TEXT NOT NULL,data JSONB NOT NULL,version INTEGER NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(kind,id));
      CREATE INDEX entities_kind ON entities(kind);
      CREATE TABLE identities(identity TEXT PRIMARY KEY,project_id TEXT NOT NULL);
      CREATE TABLE retries(scope TEXT NOT NULL,key TEXT NOT NULL,request TEXT NOT NULL,response TEXT NOT NULL,PRIMARY KEY(scope,key));
      CREATE TABLE record_search(record_id TEXT PRIMARY KEY,title TEXT NOT NULL,content TEXT NOT NULL,document TSVECTOR GENERATED ALWAYS AS(to_tsvector('simple',title || ' ' || content)) STORED);
      CREATE INDEX record_search_document ON record_search USING GIN(document);`);
      await tx`INSERT INTO schema_migrations VALUES(1,${now()})`;
      await this.checkPostgres(tx as unknown as Sql);
      if (this.pgvector) await this.initializeVectors(tx as unknown as Sql);
    });
  }
  private async initializeVectors(pg: Sql) {
    // Operators install the extension explicitly; ordinary PostgreSQL and SQLite never require it.
    const extension =
      await pg`SELECT extversion FROM pg_extension WHERE extname='vector' AND extnamespace=(SELECT oid FROM pg_namespace WHERE nspname=current_schema())`;
    if (!extension.length)
      throw new Error(
        "Install pgvector in the application schema before enabling it.",
      );
    await pg.unsafe(`CREATE TABLE IF NOT EXISTS pgvector_embeddings(id TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, model TEXT NOT NULL, dimensions INTEGER NOT NULL, content_hash TEXT NOT NULL, embedding vector NOT NULL, CHECK(vector_dims(embedding)=dimensions));
      CREATE INDEX IF NOT EXISTS pgvector_embeddings_compatibility ON pgvector_embeddings(fingerprint,model,dimensions);`);
    // Rebuild from persisted derived entities on every opt-in startup, including after a disabled interval.
    // Transaction/advisory locking makes this restart-safe and prevents partial indexes becoming visible.
    await pg`DELETE FROM pgvector_embeddings`;
    const embeddings =
      await pg`SELECT data FROM entities WHERE kind='ai_embedding'`;
    for (const row of embeddings) {
      const entity = row.data as Entity;
      const vector = entity.vector as number[];
      if (
        vector.length > 16000 ||
        !vector.every((n) => Number.isFinite(Math.fround(n))) ||
        !vector.some((n) => Math.fround(n) !== 0)
      )
        continue;
      await pg`INSERT INTO pgvector_embeddings VALUES(${entity.id},${String(entity.fingerprint)},${String(entity.model)},${Number(entity.dimensions)},${String(entity.contentHash)},${JSON.stringify(vector)}::vector)`;
    }
    await pg`SELECT id,fingerprint,model,dimensions,content_hash,vector_dims(embedding) FROM pgvector_embeddings LIMIT 0`;
  }
  private async syncEmbedding(entity: Entity) {
    if (!this.pgvector) return;
    const vector = entity.vector as number[];
    if (
      Number(entity.dimensions) > 16000 ||
      !vector.every((n) => Number.isFinite(Math.fround(n))) ||
      !vector.some((n) => Math.fround(n) !== 0)
    ) {
      await this.query(
        sql`DELETE FROM pgvector_embeddings WHERE id=${entity.id}`,
      );
      return;
    }
    await this.query(
      sql`INSERT INTO pgvector_embeddings(id,fingerprint,model,dimensions,content_hash,embedding) VALUES(${entity.id},${String(entity.fingerprint)},${String(entity.model)},${Number(entity.dimensions)},${String(entity.contentHash)},${JSON.stringify(entity.vector)}::vector) ON CONFLICT(id) DO UPDATE SET fingerprint=EXCLUDED.fingerprint,model=EXCLUDED.model,dimensions=EXCLUDED.dimensions,content_hash=EXCLUDED.content_hash,embedding=EXCLUDED.embedding`,
    );
  }
  async vectorScores(
    eligible: Entity[],
    vector: number[],
  ): Promise<{ recordId: string; score: number }[] | undefined> {
    if (
      !this.pgvector ||
      vector.length > 16000 ||
      !vector.every((n) => Number.isFinite(Math.fround(n))) ||
      !vector.some((n) => Math.fround(n) !== 0)
    )
      return undefined;
    if (!eligible.length) return [];
    return this.run(async () => {
      // Candidate identities and all compatibility fields are checked before native cosine ranking.
      const candidates = eligible.map((e) => ({
        id: e.id,
        fingerprint: String(e.fingerprint),
        model: String(e.model),
        dimensions: vector.length,
        content_hash: String(e.contentHash),
      }));
      const rows = await this.query(
        sql`WITH eligible AS MATERIALIZED (SELECT v.id,v.embedding FROM pgvector_embeddings v JOIN jsonb_to_recordset(${JSON.stringify(candidates)}::jsonb) AS c(id text,fingerprint text,model text,dimensions integer,content_hash text) ON v.id=c.id AND v.fingerprint=c.fingerprint AND v.model=c.model AND v.dimensions=c.dimensions AND v.content_hash=c.content_hash) SELECT id,1-(embedding <=> ${JSON.stringify(vector)}::vector) AS score FROM eligible ORDER BY score DESC,id`,
      );
      if (rows.length !== eligible.length) return undefined;
      const records = new Map(eligible.map((e) => [e.id, String(e.recordId)]));
      return rows.map((row) => ({
        recordId: records.get(String(row.id)) as string,
        score: Number(row.score),
      }));
    });
  }
  private checkSqlite() {
    try {
      const migrations = this.sqlite
        .prepare(
          "SELECT version,applied_at FROM schema_migrations ORDER BY version",
        )
        .all() as { version: number }[];
      if (migrations.length !== 1 || migrations[0]?.version !== 1)
        throw new Error("Invalid schema");
      for (const q of [
        "SELECT kind,id,data,version,created_at FROM entities LIMIT 0",
        "SELECT identity,project_id FROM identities LIMIT 0",
        "SELECT scope,key,request,response FROM retries LIMIT 0",
        "SELECT record_id,title,content FROM record_search WHERE record_search MATCH 'scratchpad' LIMIT 0",
      ])
        this.sqlite.prepare(q).all();
    } catch {
      throw new ApiError(
        503,
        "DATABASE_NOT_READY",
        "SQLite persistence or schema migrations are unavailable or unsupported.",
      );
    }
  }
  private async checkPostgres(pg: Sql) {
    try {
      const rows =
        await pg`SELECT version,applied_at FROM schema_migrations ORDER BY version`;
      if (rows.length !== 1 || rows[0]?.version !== 1)
        throw new Error("Invalid schema");
      for (const q of [
        "SELECT kind,id,data,version,created_at FROM entities LIMIT 0",
        "SELECT identity,project_id FROM identities LIMIT 0",
        "SELECT scope,key,request,response FROM retries LIMIT 0",
        "SELECT record_id,title,content,document FROM record_search WHERE document @@ plainto_tsquery('simple','scratchpad') LIMIT 0",
      ])
        await pg.unsafe(q);
    } catch {
      throw new ApiError(
        503,
        "DATABASE_NOT_READY",
        "PostgreSQL persistence or schema migrations are unavailable or unsupported.",
      );
    }
  }
  async assertReady() {
    try {
      await this.ready;
      if (this.pg) {
        await this.checkPostgres(this.pg);
        if (this.pgvector)
          await this
            .pg`SELECT vector_dims(embedding),fingerprint,model,dimensions,content_hash FROM pgvector_embeddings LIMIT 0`;
      } else this.checkSqlite();
    } catch {
      throw new ApiError(
        503,
        "DATABASE_NOT_READY",
        `${this.backend} persistence or schema migrations are unavailable or unsupported.`,
      );
    }
  }
  private pgDatabase(): PgDb | PgTx {
    const tx = this.context.getStore();
    if (tx && tx !== true) return tx;
    if (!this.orm) throw new Error("PostgreSQL is not configured.");
    return this.orm;
  }
  private query(query: SQL) {
    return this.pgDatabase().execute(query);
  }
  private async run<T>(fn: () => T | Promise<T>): Promise<T> {
    await this.ready;
    if (this.context.getStore()) return fn();
    if (this.pg) return fn();
    const work = this.queue.then(fn);
    this.queue = work.catch(() => {});
    return work;
  }
  async get(kind: string, key: string): Promise<Entity | undefined> {
    return this.run(async () => {
      if (this.pg)
        return (
          await this.pgDatabase()
            .select()
            .from(pgEntities)
            .where(and(eq(pgEntities.kind, kind), eq(pgEntities.id, key)))
        )[0]?.data;
      return sqliteDrizzle(this.sqlite)
        .select()
        .from(entities)
        .where(and(eq(entities.kind, kind), eq(entities.id, key)))
        .get()?.data;
    });
  }
  async list(kind: string): Promise<Entity[]> {
    return this.run(async () => {
      if (this.pg)
        return (
          await this.pgDatabase()
            .select()
            .from(pgEntities)
            .where(eq(pgEntities.kind, kind))
            .orderBy(pgEntities.createdAt, pgEntities.id)
        ).map((row) => row.data);
      return sqliteDrizzle(this.sqlite)
        .select()
        .from(entities)
        .where(eq(entities.kind, kind))
        .orderBy(entities.createdAt, entities.id)
        .all()
        .map((row) => row.data);
    });
  }
  async insert(
    kind: string,
    value: JsonObject & { id: string },
  ): Promise<Entity> {
    if (this.pgvector && kind === "ai_embedding" && !this.context.getStore())
      return this.atomic(() => this.insert(kind, value));
    return this.run(async () => {
      const entity = {
        ...value,
        createdAt:
          typeof value.createdAt === "string" ? value.createdAt : now(),
        version: typeof value.version === "number" ? value.version : 1,
      } as Entity;
      const row = {
        kind,
        id: entity.id,
        data: entity,
        version: entity.version,
        createdAt: entity.createdAt,
      };
      if (this.pg) {
        await this.pgDatabase().insert(pgEntities).values(row);
        if (kind === "ai_embedding") await this.syncEmbedding(entity);
      } else sqliteDrizzle(this.sqlite).insert(entities).values(row).run();
      return entity;
    });
  }
  async update(
    kind: string,
    entity: Entity,
    expected?: number,
  ): Promise<Entity> {
    return this.atomic(async () => {
      const previous = await this.get(kind, entity.id);
      if (!previous)
        throw new ApiError(404, "NOT_FOUND", `${kind} does not exist.`);
      if (expected !== undefined && previous.version !== expected)
        throw new ApiError(
          409,
          "CONFLICT",
          "This item changed since it was read.",
          { currentVersion: previous.version },
        );
      const next = {
        ...entity,
        version: previous.version + 1,
        updatedAt: now(),
      };
      const count = this.pg
        ? (
            await this.pgDatabase()
              .update(pgEntities)
              .set({ data: next, version: next.version })
              .where(
                and(
                  eq(pgEntities.kind, kind),
                  eq(pgEntities.id, entity.id),
                  eq(pgEntities.version, previous.version),
                ),
              )
              .returning({ id: pgEntities.id })
          ).length
        : sqliteDrizzle(this.sqlite)
            .update(entities)
            .set({ data: next, version: next.version })
            .where(
              and(
                eq(entities.kind, kind),
                eq(entities.id, entity.id),
                eq(entities.version, previous.version),
              ),
            )
            .run().changes;
      if (count !== 1)
        throw new ApiError(
          409,
          "CONFLICT",
          "Concurrent update. Read the item again.",
        );
      if (kind === "ai_embedding") await this.syncEmbedding(next);
      return next;
    });
  }
  async remove(kind: string, key: string): Promise<void> {
    if (this.pgvector && kind === "ai_embedding" && !this.context.getStore())
      return this.atomic(() => this.remove(kind, key));
    await this.run(async () => {
      if (this.pgvector && kind === "ai_embedding")
        await this.query(sql`DELETE FROM pgvector_embeddings WHERE id=${key}`);
      if (this.pg)
        await this.pgDatabase()
          .delete(pgEntities)
          .where(and(eq(pgEntities.kind, kind), eq(pgEntities.id, key)));
      else
        sqliteDrizzle(this.sqlite)
          .delete(entities)
          .where(and(eq(entities.kind, kind), eq(entities.id, key)))
          .run();
    });
  }
  async audit(
    action: string,
    entityType: string,
    entityId: string,
    actor: Actor,
    previous?: unknown,
    next?: unknown,
  ) {
    await this.insert("audit", {
      id: id("audit"),
      action,
      entityType,
      entityId,
      actor,
      previous,
      next,
    });
  }
  async atomic<T>(fn: () => T | Promise<T>): Promise<T> {
    await this.ready;
    if (this.context.getStore()) return fn();
    if (this.pg)
      return this.pgDatabase().transaction(async (tx) => {
        await tx.execute(sql`SELECT pg_advisory_xact_lock(1397772880)`);
        return this.context.run(tx, async () => fn());
      });
    return this.run(async () => {
      this.sqlite.exec("BEGIN IMMEDIATE");
      try {
        const value = await this.context.run(true, fn);
        this.sqlite.exec("COMMIT");
        return value;
      } catch (error) {
        this.sqlite.exec("ROLLBACK");
        throw error;
      }
    });
  }
  async resolveIdentity(identity: string): Promise<string | undefined> {
    return this.run(async () => {
      if (this.pg)
        return (
          await this.query(
            sql`SELECT project_id FROM identities WHERE identity=${identity}`,
          )
        )[0]?.project_id as string | undefined;
      return (
        this.sqlite
          .prepare("SELECT project_id FROM identities WHERE identity=?")
          .get(identity) as { project_id: string } | undefined
      )?.project_id;
    });
  }
  async addIdentity(identity: string, projectId: string) {
    await this.atomic(async () => {
      if (this.pg)
        await this.query(
          sql`INSERT INTO identities VALUES(${identity},${projectId}) ON CONFLICT(identity) DO NOTHING`,
        );
      else
        this.sqlite
          .prepare("INSERT OR IGNORE INTO identities VALUES(?,?)")
          .run(identity, projectId);
      const current = await this.resolveIdentity(identity);
      if (current !== projectId)
        throw new ApiError(
          409,
          "CONFLICT",
          "Project source identity belongs to another project.",
        );
    });
  }
  async indexRecord(key: string, title: string, content: string) {
    await this.run(async () => {
      if (this.pg)
        await this.query(
          sql`INSERT INTO record_search(record_id,title,content) VALUES(${key},${title},${content})`,
        );
      else
        this.sqlite
          .prepare(
            "INSERT INTO record_search(record_id,title,content) VALUES(?,?,?)",
          )
          .run(key, title, content);
    });
  }
  async searchRecords(terms: string[]): Promise<string[]> {
    return this.run(async () => {
      if (this.pg)
        return (
          await this.query(
            sql`SELECT record_id FROM record_search WHERE document @@ plainto_tsquery('simple',${terms.join(" ")})`,
          )
        ).map((r) => String(r.record_id));
      return (
        this.sqlite
          .prepare(
            "SELECT record_id FROM record_search WHERE record_search MATCH ?",
          )
          .all(
            terms.map((t) => `"${t.replaceAll('"', '""')}"`).join(" AND "),
          ) as { record_id: string }[]
      ).map((r) => r.record_id);
    });
  }
  async getRetry(
    scope: string,
    key: string,
  ): Promise<{ request: string; response: string } | undefined> {
    return this.run(async () =>
      this.pg
        ? ((
            await this.query(
              sql`SELECT request,response FROM retries WHERE scope=${scope} AND key=${key}`,
            )
          )[0] as { request: string; response: string } | undefined)
        : (this.sqlite
            .prepare(
              "SELECT request,response FROM retries WHERE scope=? AND key=?",
            )
            .get(scope, key) as
            | { request: string; response: string }
            | undefined),
    );
  }
  async saveRetry(
    scope: string,
    key: string,
    request: string,
    response: string,
  ) {
    await this.run(async () => {
      if (this.pg)
        await this.query(
          sql`INSERT INTO retries VALUES(${scope},${key},${request},${response})`,
        );
      else
        this.sqlite
          .prepare("INSERT INTO retries VALUES(?,?,?,?)")
          .run(scope, key, request, response);
    });
  }
  async backup(destination: string) {
    if (this.pg)
      throw new ApiError(
        400,
        "BACKUP_EXTERNAL",
        "Use PostgreSQL pg_dump for a complete database backup. Native export remains available.",
      );
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
    privateDirectory(dirname(destination));
    closeSync(openSync(destination, "wx", 0o600));
    try {
      await this.run(() => this.sqlite.backup(destination));
      chmodSync(destination, 0o600);
    } catch (error) {
      unlinkSync(destination);
      throw error;
    }
  }
  async close() {
    await this.queue;
    if (this.pg) await this.pg.end({ timeout: 5 });
    else if (this.sqlite.open) this.sqlite.close();
  }
}
