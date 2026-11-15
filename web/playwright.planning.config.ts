import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  testMatch: 'planning*.spec.ts',
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 45000,
  outputDir: '../.artifacts/agents/PLAN08/browser-output',
  reporter: [['list']],
  use: {
    baseURL: process.env.PLANNING_BASE_URL ?? 'http://host.docker.internal:7082',
    locale: 'en-US',
    timezoneId: 'America/Chicago',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'desktop-light',
      use: {
        ...devices['Desktop Chrome'],
        viewport: { width: 1440, height: 1000 },
        colorScheme: 'light',
      },
    },
    {
      name: 'desktop-dark',
      use: {
        ...devices['Desktop Chrome'],
        viewport: { width: 1440, height: 1000 },
        colorScheme: 'dark',
      },
    },
    { name: 'phone-light', use: { ...devices['Pixel 7'], colorScheme: 'light' } },
    { name: 'phone-dark', use: { ...devices['Pixel 7'], colorScheme: 'dark' } },
  ],
});
