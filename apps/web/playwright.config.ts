import { defineConfig } from "@playwright/test";

const baseURL = process.env.WEB_BASE_URL ?? "http://127.0.0.1:3000";
const browserChannel = process.env.PLAYWRIGHT_BROWSER_CHANNEL ?? "chrome";
const headless = process.env.PLAYWRIGHT_HEADLESS !== "false";

export default defineConfig({
  testDir: "./e2e",
  timeout: 45_000,
  expect: {
    timeout: 12_000,
  },
  fullyParallel: false,
  workers: 1,
  reporter: "list",
  use: {
    baseURL,
    channel: browserChannel,
    headless,
    trace: "retain-on-failure",
    viewport: {
      width: 1280,
      height: 720,
    },
  },
});
