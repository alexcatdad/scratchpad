import { execFileSync } from "node:child_process";
import {
  chmodSync,
  linkSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, it } from "vitest";
import { Store } from "./store";

it("creates private SQLite storage under a permissive umask and preserves it on reopen and backup", async () => {
  const root = mkdtempSync(join(tmpdir(), "scratchpad-permissions-"));
  const previous = process.umask(0o022);
  const path = join(root, "private", "db.sqlite");
  let store: Store | undefined;
  try {
    chmodSync(root, 0o755);
    store = new Store(path);
    expect(statSync(join(root, "private")).mode & 0o777).toBe(0o700);
    for (const suffix of ["", "-wal", "-shm"])
      expect(statSync(path + suffix).mode & 0o777).toBe(0o600);
    await store.close();
    store = new Store(path);
    await store.backup(join(root, "backups", "copy.sqlite"));
    expect(statSync(join(root, "backups")).mode & 0o777).toBe(0o700);
    expect(statSync(join(root, "backups", "copy.sqlite")).mode & 0o777).toBe(
      0o600,
    );
    expect(statSync(root).mode & 0o777).toBe(0o755);
  } finally {
    await store?.close();
    process.umask(previous);
    rmSync(root, { recursive: true, force: true });
  }
});

it("refuses a permissive existing database without opening or changing it", async () => {
  const root = mkdtempSync(join(tmpdir(), "scratchpad-permissions-"));
  const path = join(root, "db.sqlite");
  const store = new Store(path);
  await store.close();
  chmodSync(path, 0o644);
  const original = readFileSync(path);
  try {
    expect(() => new Store(path)).toThrow(/owner-only.*0600/);
    expect(readFileSync(path)).toEqual(original);
    expect(statSync(path).mode & 0o777).toBe(0o644);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

it.each(["directory", "-wal", "-shm", "-journal"])(
  "rejects permissive existing %s storage before touching the database",
  async (target) => {
    const root = mkdtempSync(join(tmpdir(), "scratchpad-permissions-"));
    const path = join(root, "db.sqlite");
    const store = new Store(path);
    await store.close();
    const original = readFileSync(path);
    try {
      if (target === "directory") chmodSync(root, 0o755);
      else
        writeFileSync(path + target, "synthetic unsafe companion", {
          mode: 0o644,
        });
      expect(() => new Store(path)).toThrow(/owner-only/);
      expect(readFileSync(path)).toEqual(original);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  },
);

it.each(["symlink", "hardlink", "fifo"])(
  "rejects %s database targets without blocking",
  (kind) => {
    const root = mkdtempSync(join(tmpdir(), "scratchpad-permissions-"));
    const path = join(root, "db.sqlite");
    try {
      const target = join(root, "synthetic");
      writeFileSync(target, "synthetic", { mode: 0o600 });
      if (kind === "symlink") symlinkSync(target, path);
      else if (kind === "hardlink") linkSync(target, path);
      else execFileSync("mkfifo", [path]);
      expect(() => new Store(path)).toThrow();
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  },
);

it("refuses backups into a shared existing directory without changing it", async () => {
  const root = mkdtempSync(join(tmpdir(), "scratchpad-permissions-"));
  const store = new Store(join(root, "db.sqlite"));
  const shared = join(root, "shared");
  mkdirSync(shared, { mode: 0o755 });
  try {
    await expect(store.backup(join(shared, "copy.sqlite"))).rejects.toThrow(
      /owner-only/,
    );
    expect(statSync(shared).mode & 0o777).toBe(0o755);
  } finally {
    await store.close();
    rmSync(root, { recursive: true, force: true });
  }
});

it.skipIf(process.platform !== "linux" || !process.env.CI)(
  "denies a separate local principal access to live SQLite companions and backups",
  async () => {
    const root = mkdtempSync(join(tmpdir(), "scratchpad-principal-"));
    chmodSync(root, 0o755);
    const control = join(root, "public-control");
    writeFileSync(control, "synthetic-readable-control", { mode: 0o644 });
    const path = join(root, "private", "db.sqlite");
    const store = new Store(path);
    try {
      await store.insert("ai_settings", {
        id: "global",
        apiKey: "synthetic-private-only",
      });
      const backup = join(root, "backup", "copy.sqlite");
      await store.backup(backup);
      execFileSync(
        "sudo",
        [
          "-n",
          "-u",
          "nobody",
          process.execPath,
          "-e",
          "const fs=require('node:fs');if(fs.readFileSync(process.argv[1],'utf8')!=='synthetic-readable-control')process.exit(2);for(const p of process.argv.slice(2)){try{fs.readFileSync(p);process.exit(3)}catch(e){if(e.code!=='EACCES')process.exit(4)}}",
          control,
          path,
          `${path}-wal`,
          `${path}-shm`,
          backup,
        ],
        { stdio: "pipe" },
      );
    } finally {
      await store.close();
      rmSync(root, { recursive: true, force: true });
    }
  },
);

it.skipIf(process.platform !== "darwin")(
  "rejects macOS ACL grants even when mode bits are private",
  () => {
    const root = mkdtempSync(join(tmpdir(), "scratchpad-acl-"));
    const path = join(root, "db.sqlite");
    writeFileSync(path, "synthetic", { mode: 0o600 });
    try {
      execFileSync("/bin/chmod", ["+a", "everyone allow read", path]);
      expect(statSync(path).mode & 0o777).toBe(0o600);
      expect(() => new Store(path)).toThrow(/remove extended ACLs/);
    } finally {
      execFileSync("/bin/chmod", ["-N", path]);
      rmSync(root, { recursive: true, force: true });
    }
  },
);

it("preserves live private state through an administrator-process online backup", async () => {
  const root = mkdtempSync(join(tmpdir(), "scratchpad-online-backup-"));
  const path = join(root, "db.sqlite");
  const backup = join(root, "copy.sqlite");
  const store = new Store(path);
  let restored: Store | undefined;
  try {
    await store.insert("ai_settings", {
      id: "global",
      apiKey: "synthetic-retained-secret",
    });
    execFileSync(
      process.execPath,
      [
        "--import",
        "tsx",
        "--input-type=module",
        "-e",
        "const {Store}=await import(process.argv[1]);const store=new Store(process.argv[2]);await store.backup(process.argv[3]);await store.close();",
        new URL("./store.ts", import.meta.url).href,
        path,
        backup,
      ],
      { stdio: "pipe" },
    );
    restored = new Store(backup);
    expect((await restored.get("ai_settings", "global"))?.apiKey).toBe(
      "synthetic-retained-secret",
    );
    expect((await store.get("ai_settings", "global"))?.apiKey).toBe(
      "synthetic-retained-secret",
    );
  } finally {
    await restored?.close();
    await store.close();
    rmSync(root, { recursive: true, force: true });
  }
});
