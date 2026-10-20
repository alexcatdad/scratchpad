import { spawn } from "node:child_process";

const env = {
  ...process.env,
  SCRATCHPAD_LISTEN_ADDR:
    process.env.SCRATCHPAD_LISTEN_ADDR ?? "127.0.0.1:3000",
  SCRATCHPAD_PUBLIC_URL:
    process.env.SCRATCHPAD_PUBLIC_URL ?? "http://localhost:3000",
  SCRATCHPAD_DASHBOARD_URL:
    process.env.SCRATCHPAD_DASHBOARD_URL ?? "http://127.0.0.1:4000",
  SCRATCHPAD_UI_ONLY: "true",
  HOST: "127.0.0.1",
  PORT: process.env.SCRATCHPAD_UI_PORT ?? "4000",
  SCRATCHPAD_GITHUB_CLIENT_ID: "",
  SCRATCHPAD_GITHUB_CLIENT_SECRET: "",
  SCRATCHPAD_ENCRYPTION_KEY_FILE: "",
  SCRATCHPAD_DATABASE_URL: "",
};

function run(file, args, options = {}) {
  return spawn(file, args, { env, stdio: "inherit", ...options });
}

const build = run("npm", ["run", "build", "-w", "@scratchpad/web"]);
const buildCode = await new Promise((resolve, reject) => {
  build.once("error", reject);
  build.once("close", resolve);
});
if (buildCode !== 0) process.exit(buildCode ?? 1);

const children = [
  run("go", ["-C", "backend", "run", "./cmd/scratchpad-server", "serve"]),
  run(process.execPath, ["apps/web/server.mjs"]),
];
let stopping = false;
let exitCode = 0;
function stop(code = 0) {
  if (stopping) return;
  stopping = true;
  exitCode = code;
  for (const child of children)
    if (child.exitCode === null && !child.signalCode) child.kill("SIGTERM");
}
process.once("SIGINT", () => stop());
process.once("SIGTERM", () => stop());
await Promise.all(
  children.map(
    (child) =>
      new Promise((done) => {
        child.once("error", () => {
          stop(1);
          done();
        });
        child.once("close", (code) => {
          if (!stopping) stop(code ?? 1);
          done();
        });
      }),
  ),
);
process.exitCode = exitCode;
