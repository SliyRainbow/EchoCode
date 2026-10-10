import { defineConfig } from "@playwright/test";

const testPort = process.env.ECHOCODE_TEST_PORT || "34117";

export default defineConfig({
  testDir: "./e2e",
  testMatch: "**/*.e2e.ts",
  fullyParallel: true,
  workers: 2,
  timeout: 30_000,
  reporter: "list",
  use: {
    baseURL: `http://127.0.0.1:${testPort}`,
    browserName: "chromium",
    channel: process.env.PLAYWRIGHT_BROWSER_CHANNEL || (process.platform === "win32" ? "msedge" : undefined),
    viewport: { width: 1440, height: 900 },
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  webServer: {
    command: `node node_modules/vite/bin/vite.js --host 127.0.0.1 --port ${testPort} --strictPort`,
    url: `http://127.0.0.1:${testPort}`,
    reuseExistingServer: false,
  },
});
