import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

test("formula selects all four exact published archives and refuses downgrade", () => {
  const temp = mkdtempSync(join(tmpdir(), "scratchpad-formula-"));
  try {
    const assets = join(temp, "assets");
    const tap = join(temp, "tap");
    mkdirSync(assets);
    for (const os of ["darwin", "linux"]) {
      for (const arch of ["arm64", "amd64"]) {
        const name = `scratchpad-mcp-v1.2.3-${os}-${arch}.${os === "darwin" ? "zip" : "tar.gz"}`;
        writeFileSync(join(assets, name), `verified-${os}-${arch}`);
      }
    }
    const run = (tag) =>
      spawnSync(
        process.execPath,
        ["scripts/release-formula.mjs", tag, assets, tap],
        { encoding: "utf8" },
      );
    const first = run("v1.2.3");
    assert.equal(first.status, 0, first.stderr);
    const formula = readFileSync(
      join(tap, "Formula/scratchpad-mcp.rb"),
      "utf8",
    );
    for (const os of ["darwin", "linux"]) {
      for (const arch of ["arm64", "amd64"]) {
        assert.ok(
          formula.includes(`/v1.2.3/scratchpad-mcp-v1.2.3-${os}-${arch}.`),
        );
        assert.ok(
          formula.includes(
            createHash("sha256").update(`verified-${os}-${arch}`).digest("hex"),
          ),
        );
      }
    }
    assert.equal(run("v1.2.3").status, 0, "identical recovery is safe");
    assert.notEqual(run("v1.2.2").status, 0, "older patch is rejected");
    assert.notEqual(run("v0.9.99").status, 0, "older major is rejected");
    assert.notEqual(
      run("v1.2.3-rc.1").status,
      0,
      "prerelease is outside supported stable policy",
    );
    assert.equal(
      readFileSync(join(tap, "Formula/scratchpad-mcp.rb"), "utf8"),
      formula,
    );
  } finally {
    rmSync(temp, { recursive: true, force: true });
  }
});
