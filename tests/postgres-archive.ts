import { execFileSync } from "node:child_process";

// Use clients from the owned CI service or matching local PostgreSQL clients.
// Never put connection passwords in argv or print archive contents.
export function postgresArchive(
  tool: "pg_dump" | "pg_restore",
  databaseUrl: string,
  input?: Buffer,
): Buffer {
  const url = new URL(databaseUrl);
  const container = process.env.TEST_POSTGRES_CONTAINER_ID;
  const env = {
    ...process.env,
    PGHOST: container ? "127.0.0.1" : url.hostname,
    PGPORT: container ? "5432" : url.port || "5432",
    PGUSER: decodeURIComponent(url.username),
    PGPASSWORD: decodeURIComponent(url.password),
    PGDATABASE: decodeURIComponent(url.pathname.slice(1)),
    PGSSLMODE: url.searchParams.get("sslmode") || "prefer",
  };
  const args =
    tool === "pg_dump"
      ? ["--format=custom", "--no-owner", "--no-acl"]
      : [
          "--exit-on-error",
          "--no-owner",
          "--no-acl",
          `--dbname=${env.PGDATABASE}`,
        ];
  return execFileSync(
    container ? "docker" : tool,
    container
      ? [
          "exec",
          "-i",
          ...[
            "PGHOST",
            "PGPORT",
            "PGUSER",
            "PGPASSWORD",
            "PGDATABASE",
            "PGSSLMODE",
          ].flatMap((name) => ["--env", name]),
          container,
          tool,
          ...args,
        ]
      : args,
    { env, input, timeout: 30_000, stdio: ["pipe", "pipe", "ignore"] },
  );
}
