import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

test("release-cask renders the pinned arm64 app archive and refuses downgrades", () => {
  const root = mkdtempSync(join(tmpdir(), "scratchpad-release-cask-"));
  try {
    const archive = join(root, "scratchpad-v1.2.3-darwin-arm64.zip");
    const tap = join(root, "tap");
    mkdirSync(tap);
    const bytes = Buffer.from("synthetic signed app archive");
    writeFileSync(archive, bytes);
    const result = (tag) => spawnSync(
      process.execPath,
      ["scripts/release-cask.mjs", tag, archive, tap],
      { cwd: process.cwd(), encoding: "utf8" },
    );

    const first = result("v1.2.3");
    assert.equal(first.status, 0, first.stderr);
    const cask = readFileSync(join(tap, "Casks/scratchpad.rb"), "utf8");
    assert.match(cask, /cask "scratchpad" do/);
    assert.match(cask, /version "1\.2\.3"/);
    assert.match(cask, new RegExp(`sha256 "${createHash("sha256").update(bytes).digest("hex")}"`));
    assert.match(cask, /scratchpad-v1\.2\.3-darwin-arm64\.zip/);
    assert.match(cask, /depends_on arch: :arm64/);
    assert.match(cask, /depends_on macos: :ventura/);
    assert.match(cask, /app "Scratchpad\.app"/);
    assert.match(cask, /uninstall quit: "com\.alexcatdad\.scratchpad"/);
    assert.doesNotMatch(cask, /preflight|postflight|prepare-upgrade|finish-upgrade/);
    assert.doesNotMatch(cask, /verified:|no_quarantine|quarantine/);

    const downgrade = result("v1.2.2");
    assert.notEqual(downgrade.status, 0);
    assert.match(downgrade.stderr, /Refusing to downgrade/);
    assert.equal(readFileSync(join(tap, "Casks/scratchpad.rb"), "utf8"), cask);
    assert.notEqual(result("invalid").status, 0);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
