import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:10112',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'cd ../.. && make server-dev && ./bin/saker --server --server-addr :10112',
    url: 'http://localhost:10112/health',
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
});