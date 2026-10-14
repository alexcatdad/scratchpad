import { resolve } from "node:path";
import { tanstackStart } from "@tanstack/react-start/plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, normalizePath } from "vite";

const database = normalizePath(
  resolve(process.env.SCRATCHPAD_DATABASE_PATH ?? "data/scratchpad.sqlite"),
).replace(/[?*()[\]{}!+@]/g, "\\$&");

export default defineConfig({
  publicDir: false,
  server: {
    host: "127.0.0.1",
    port: 3000,
    fs: {
      deny: [
        "**/.env",
        "**/.env.*",
        "**/*.{crt,pem,key,p12,pfx,cer,der}",
        "**/.npmrc",
        "**/.yarnrc.yml",
        "**/.git/**",
        "**/data/**",
        "**/*.{sqlite,sqlite3,db}{,-wal,-shm,-journal}",
        database,
        `${database}-wal`,
        `${database}-shm`,
        `${database}-journal`,
      ],
    },
  },
  plugins: [tanstackStart(), react()],
});
