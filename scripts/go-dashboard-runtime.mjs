import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";

const [binary, ...provided] = process.argv.slice(2);
if (!binary) throw new Error("Supply the Go server executable.");
const command = provided.length ? provided : ["serve"];
const serving = command.length === 1 && command[0] === "serve";
const uiPort = process.env.SCRATCHPAD_UI_PORT || "4000";
if (
  serving &&
  (!/^\d+$/.test(uiPort) || Number(uiPort) < 1 || Number(uiPort) > 65535)
)
  throw new Error("SCRATCHPAD_UI_PORT must be a valid port.");

const children = [];
let stopping = false;
let exitCode = 0;
let deadline;
function stop() {
  if (stopping) return;
  stopping = true;
  for (const child of children)
    if (child.exitCode === null && !child.signalCode) child.kill("SIGTERM");
  deadline = setTimeout(() => {
    exitCode = 1;
    for (const child of children)
      if (child.exitCode === null && !child.signalCode) child.kill("SIGKILL");
  }, 15_000);
}
process.once("SIGTERM", stop);
process.once("SIGINT", stop);

const processes = [
  {
    file: binary,
    args: command,
    env: serving
      ? {
          ...process.env,
          SCRATCHPAD_DASHBOARD_URL: `http://127.0.0.1:${uiPort}`,
        }
      : process.env,
  },
];
if (serving)
  processes.push({
    file: process.execPath,
    args: [fileURLToPath(new URL("../apps/web/server.mjs", import.meta.url))],
    env: {
      ...process.env,
      SCRATCHPAD_UI_ONLY: "true",
      HOST: "127.0.0.1",
      PORT: uiPort,
      SCRATCHPAD_GITHUB_CLIENT_ID: "",
      SCRATCHPAD_GITHUB_CLIENT_SECRET: "",
      SCRATCHPAD_ENCRYPTION_KEY_FILE: "",
      SCRATCHPAD_DATABASE_URL: "",
    },
  });
await Promise.all(
  processes.map(({ file, args, env }) => {
    const child = spawn(file, args, { env, stdio: "inherit" });
    children.push(child);
    return new Promise((done) => {
      child.once("error", () => {
        console.error("Application process could not start.");
        exitCode = 1;
        stop();
      });
      child.once("close", (code) => {
        if (!stopping) {
          exitCode = serving ? 1 : (code ?? 1);
          stop();
        } else if (!serving && code !== 0) exitCode = 1;
        done();
      });
    });
  }),
);
clearTimeout(deadline);
process.exitCode = exitCode;
