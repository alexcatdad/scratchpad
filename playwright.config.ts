import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./tests",
  timeout: 120_000,
  workers: 1,
  projects: [
    { name: "retained", testIgnore: "**/go-*.spec.ts" },
    {
      name: "go-native",
      testMatch: "**/go-*.spec.ts",
      testIgnore: "**/go-container.spec.ts",
    },
    { name: "go-container", testMatch: "**/go-container.spec.ts" },
  ],
  use: {
    headless: true,
    actionTimeout: 10_000,
    screenshot: "only-on-failure",
    viewport: { width: 1440, height: 1000 },
  },
  reporter: "list",
});
