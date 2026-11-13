import { defineConfig } from "@playwright/test";

// E2E against the real binary (tests/e2e/allmend.ts starts and stops it, to
// make the server unreachable). The installed Chrome is used by default;
// PLAYWRIGHT_CHANNEL="" uses Playwright's own Chromium.
const channel = process.env["PLAYWRIGHT_CHANNEL"] ?? "chrome";

export default defineConfig({
  testDir: "tests/e2e",
  globalSetup: "./tests/e2e/global-setup.ts",
  workers: 1,
  fullyParallel: false,
  retries: 0,
  reporter: [["list"]],
  timeout: 60_000,
  use: {
    ...(channel ? { channel } : {}),
    viewport: { width: 1200, height: 800 },
    locale: "de-CH",
    timezoneId: "Europe/Zurich",
  },
});
