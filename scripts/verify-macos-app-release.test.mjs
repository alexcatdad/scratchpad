import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const hash = (value) => createHash("sha256").update(value).digest("hex");

test("app verification rejects mismatched provenance, altered assets and unsafe archive roots before Apple checks", () => {
  const root = mkdtempSync(join(tmpdir(), "scratchpad-app-verification-"));
  try {
    const bin = join(root, "bin");
    mkdirSync(bin);
    writeFileSync(
      join(bin, "uname"),
      '#!/bin/sh\ncase "$1" in -s) echo Darwin;; -m) echo arm64;; esac\n',
    );
    chmodSync(join(bin, "uname"), 0o755);
    const archive = "not an archive";
    const receipt = JSON.stringify({ status: "Accepted", id: "synthetic" });
    const manifest = {
      tag: "v1.2.3",
      sourceSha: "synthetic-source",
      teamId: "SYNTHETIC",
      architecture: "arm64",
      artifacts: {
        app: { sha256: hash(archive), receiptSha256: hash(receipt) },
      },
    };
    writeFileSync(join(root, "scratchpad-v1.2.3-darwin-arm64.zip"), archive);
    writeFileSync(join(root, "notarization-app-v1.2.3.json"), receipt);
    for (const patch of [
      { tag: "v9.9.9" },
      { sourceSha: "other-source" },
      { teamId: "OTHER" },
      { architecture: "amd64" },
      {
        artifacts: {
          app: { ...manifest.artifacts.app, sha256: hash("altered") },
        },
      },
      {
        artifacts: {
          app: { ...manifest.artifacts.app, receiptSha256: hash("altered") },
        },
      },
    ]) {
      writeFileSync(
        join(root, "macos-app-release.json"),
        JSON.stringify({ ...manifest, ...patch }),
      );
      const result = run();
      assert.notEqual(result.status, 0);
      assert.doesNotMatch(result.stderr, /tar:|codesign|spctl/);
    }
    // A real archive with valid checksums must still reject an unexpected root.
    writeFileSync(join(root, "unexpected.txt"), "synthetic");
    const packed = spawnSync("tar", ["-cf", "-", "unexpected.txt"], {
      cwd: root,
    });
    assert.equal(packed.status, 0);
    writeFileSync(
      join(root, "scratchpad-v1.2.3-darwin-arm64.zip"),
      packed.stdout,
    );
    manifest.artifacts.app.sha256 = hash(packed.stdout);
    writeFileSync(
      join(root, "macos-app-release.json"),
      JSON.stringify(manifest),
    );
    const rejectedRoot = run();
    assert.notEqual(rejectedRoot.status, 0);
    assert.doesNotMatch(
      rejectedRoot.stderr,
      /codesign|spctl|missing app metadata/,
    );

    function run() {
      return spawnSync("bash", ["scripts/verify-macos-app-release.sh"], {
        encoding: "utf8",
        env: {
          ...process.env,
          PATH: `${bin}:${process.env.PATH}`,
          RELEASE_TAG: "v1.2.3",
          EXPECTED_SHA: "synthetic-source",
          APPLE_TEAM_ID: "SYNTHETIC",
          ASSET_DIR: root,
        },
      });
    }
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
