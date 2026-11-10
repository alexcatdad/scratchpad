import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

// The Go server embeds the built dashboard, so every server binary the tests
// compile needs the static build in place first. A prebuilt binary
// (SCRATCHPAD_E2E_SERVER_BINARY) already carries its own.
export default function globalSetup() {
  if (process.env.SCRATCHPAD_E2E_SKIP_WEB_BUILD === "1") return;
  execFileSync("npm", ["run", "build", "-w", "@scratchpad/web"], {
    cwd: resolve(import.meta.dirname, ".."),
    stdio: "inherit",
    timeout: 120_000,
  });
}
