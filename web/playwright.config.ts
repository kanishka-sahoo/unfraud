import { defineConfig } from "@playwright/test";

// Runs against a deployed console: `make console` (port-forward to :3000), or
// set CONSOLE_URL. The cluster needs a Profile with Cases and a graph (e.g. tabformer).
export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  retries: 0,
  use: { baseURL: process.env.CONSOLE_URL ?? "http://localhost:3000", viewport: { width: 1440, height: 1000 } },
  reporter: [["list"]],
});
