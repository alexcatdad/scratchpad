import { tanstackStart } from "@tanstack/react-start/plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  server: { host: "0.0.0.0", port: 3000 },
  plugins: [tanstackStart(), react()],
});
