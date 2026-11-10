import { resolve } from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig, normalizePath } from "vite";

const database = normalizePath(
  resolve(process.env.SCRATCHPAD_DATABASE_PATH ?? "data/scratchpad.sqlite"),
).replace(/[?*()[\]{}!+@]/g, "\\$&");

// The build is static files only. It is written into the Go module, where
// backend/internal/httpapi/dashboard.go embeds it into the server binary.
export default defineConfig({
  publicDir: false,
  build: {
    outDir: "../../backend/internal/httpapi/dashboard/dist",
    emptyOutDir: true,
  },
  server: {
    host: "127.0.0.1",
    port: 4000,
    strictPort: true,
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
  plugins: [react()],
});
