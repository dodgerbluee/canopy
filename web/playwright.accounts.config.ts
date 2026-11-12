import { defineConfig, devices } from '@playwright/test';

// Point only at a disposable, empty application deployment. The first assertion
// refuses an already configured garden before making any data changes.
export default defineConfig({
  testDir: './e2e',
  testMatch: 'accounts.spec.ts',
  workers: 1,
  retries: 0,
  timeout: 90_000,
  reporter: [['list']],
  use: {
    baseURL: process.env.AUTH_E2E_URL ?? 'http://host.docker.internal:7081',
    ...devices['Desktop Chrome'],
    viewport: { width: 1440, height: 1000 },
  },
});
