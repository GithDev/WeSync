// @ts-check
import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  workers: 1, // test suites share live WeSync instances — must run sequentially
  use: {
    ignoreHTTPSErrors: true, // self-signed TLS certs in dev
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
