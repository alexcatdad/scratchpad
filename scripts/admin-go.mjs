import { spawnSync } from "node:child_process";

const binary = process.env.SCRATCHPAD_SERVER_BINARY;
const args = process.argv.slice(2);
const result = spawnSync(
  binary || "go",
  binary ? args : ["-C", "backend", "run", "./cmd/scratchpad-server", ...args],
  { stdio: "inherit" },
);
if (result.error) console.error("Go administrator command could not start.");
process.exitCode = result.status ?? 1;
