import { readFileSync, rmSync } from "node:fs";
import { resolve } from "node:path";
import { expect, test } from "@playwright/test";
import { createWorkflowHarness } from "./workflow-harness";

async function boundedStop(
  harness: ReturnType<typeof createWorkflowHarness>,
  deadline = 10_000,
) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    await Promise.race([
      harness.stop(),
      new Promise<never>((_, reject) => {
        timer = setTimeout(
          () => reject(new Error("Workflow stop did not finish")),
          deadline,
        );
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

test("cleaning one workflow scope preserves another scope in the same worker", async () => {
  const first = createWorkflowHarness();
  const second = createWorkflowHarness();
  try {
    await first.start();
    await boundedStop(first);
    // Repeated stop must finish even when Linux reports signalCode instead of
    // exitCode for the already terminated subprocess.
    await boundedStop(first, 1_000);
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
    const stopped = await Promise.allSettled([
      boundedStop(first),
      boundedStop(second),
    ]);
    first.cleanupDocker();
    second.cleanupDocker();
    rmSync(first.temporary, { recursive: true, force: true });
    rmSync(second.temporary, { recursive: true, force: true });
    expect(stopped.map((result) => result.status)).toEqual([
      "fulfilled",
      "fulfilled",
    ]);
  }
});
