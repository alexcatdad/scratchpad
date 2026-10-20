import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import {
  chmodSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

const root = resolve(import.meta.dirname, "..");
const node = process.execPath;

test("container admin forwards arguments and Go configuration to the installed binary", () => {
  const dir = mkdtempSync(join(tmpdir(), "scratchpad-admin-entry-"));
  try {
    const binary = join(dir, "server");
    writeFileSync(
      binary,
      `#!${node}\nconsole.log(JSON.stringify({args:process.argv.slice(2),database:process.env.SCRATCHPAD_DATABASE_URL,key:process.env.SCRATCHPAD_ENCRYPTION_KEY_FILE}));\n`,
    );
    chmodSync(binary, 0o700);
    const result = spawnSync(
      node,
      [
        resolve(root, "scripts/admin-go.mjs"),
        "backup",
        "/private/backup.sqlite",
      ],
      {
        env: {
          ...process.env,
          SCRATCHPAD_SERVER_BINARY: binary,
          SCRATCHPAD_DATABASE_URL: "synthetic-database",
          SCRATCHPAD_ENCRYPTION_KEY_FILE: "/private/key",
        },
        encoding: "utf8",
      },
    );
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout), {
      args: ["backup", "/private/backup.sqlite"],
      database: "synthetic-database",
      key: "/private/key",
    });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

for (const phase of ["npm", "go"]) {
  test(`development termination during ${phase} build prevents subsequent startup`, {
    timeout: 15000,
  }, async () => {
    const dir = mkdtempSync(join(tmpdir(), "scratchpad-dev-entry-"));
    const marker = join(dir, "started");
    for (const command of ["npm", "go"]) {
      const binary = join(dir, command);
      writeFileSync(
        binary,
        `#!${node}\nimport {writeFileSync} from 'node:fs';\n${command === phase ? `writeFileSync(${JSON.stringify(marker)},'ready');process.on('SIGTERM',()=>process.exit(0));setInterval(()=>{},100);` : "process.exit(0);"}\n`,
      );
      chmodSync(binary, 0o700);
    }
    const child = spawn(node, [resolve(root, "scripts/dev-go.mjs")], {
      env: { ...process.env, PATH: `${dir}:${process.env.PATH}` },
      stdio: "ignore",
    });
    const exited = new Promise((done, reject) => {
      child.once("error", reject);
      child.once("close", done);
    });
    try {
      const until = Date.now() + 10000;
      while (Date.now() < until) {
        try {
          if (readFileSync(marker, "utf8") === "ready") break;
        } catch {}
        await new Promise((done) => setTimeout(done, 20));
      }
      assert.equal(readFileSync(marker, "utf8"), "ready");
      child.kill("SIGTERM");
      assert.equal(await exited, 0);
    } finally {
      if (child.exitCode === null) child.kill("SIGKILL");
      await exited;
      rmSync(dir, { recursive: true, force: true });
    }
  });
}

test("development termination removes a descendant after its build leader exits", {
  timeout: 15000,
}, async () => {
  const dir = mkdtempSync(join(tmpdir(), "scratchpad-dev-descendant-"));
  const marker = join(dir, "descendant");
  const binary = join(dir, "npm");
  const descendant = `require('node:fs').writeFileSync(${JSON.stringify(marker)},String(process.pid));process.on('SIGTERM',()=>{});setInterval(()=>{},100);`;
  writeFileSync(
    binary,
    `#!${node}\nimport{spawn}from'node:child_process';spawn(process.execPath,['-e',${JSON.stringify(descendant)}],{stdio:'ignore'});process.on('SIGTERM',()=>process.exit(0));setInterval(()=>{},100);\n`,
  );
  chmodSync(binary, 0o700);
  const child = spawn(node, [resolve(root, "scripts/dev-go.mjs")], {
    env: { ...process.env, PATH: `${dir}:${process.env.PATH}` },
    stdio: "ignore",
  });
  const exited = new Promise((done, reject) => {
    child.once("error", reject);
    child.once("close", done);
  });
  let pid;
  try {
    const until = Date.now() + 10000;
    while (Date.now() < until) {
      try {
        pid = Number(readFileSync(marker, "utf8"));
        break;
      } catch {}
      await new Promise((done) => setTimeout(done, 20));
    }
    assert.ok(pid);
    child.kill("SIGTERM");
    assert.equal(await exited, 0);
    let alive = true;
    for (let attempt = 0; attempt < 50; attempt++) {
      try {
        process.kill(pid, 0);
      } catch {
        alive = false;
        break;
      }
      await new Promise((done) => setTimeout(done, 20));
    }
    assert.equal(
      alive,
      false,
      "TERM-ignoring descendant survived build shutdown",
    );
  } finally {
    if (child.exitCode === null) child.kill("SIGKILL");
    await exited;
    if (pid) {
      try {
        process.kill(pid, "SIGKILL");
      } catch {}
    }
    rmSync(dir, { recursive: true, force: true });
  }
});
