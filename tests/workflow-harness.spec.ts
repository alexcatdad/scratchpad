import { readFileSync, rmSync } from "node:fs";
import { resolve } from "node:path";
import { expect, test } from "@playwright/test";
import { createWorkflowHarness } from "./workflow-harness";

test("cleaning one workflow scope preserves another scope in the same worker", async () => {
  const first = createWorkflowHarness();
  const second = createWorkflowHarness();
  try {
    await first.start();
    await first.stop();
    first.cleanupDocker();
    rmSync(first.temporary, { recursive: true, force: true });
    // A new suite can still generate its disposable identity and start its own
    // database/container after the earlier suite has finished cleanup.
    const key = resolve(second.temporary, "identity");
    second.command("ssh-keygen", ["-q", "-t", "ed25519", "-N", "", "-f", key]);
    expect(readFileSync(`${key}.pub`, "utf8")).toMatch(/^ssh-ed25519 /);
    await second.start();
    expect((await fetch(`${second.origin}/ready`)).ok).toBe(true);
  } finally {
    await first.stop();
    first.cleanupDocker();
    await second.stop();
    second.cleanupDocker();
    rmSync(first.temporary, { recursive: true, force: true });
    rmSync(second.temporary, { recursive: true, force: true });
  }
});
