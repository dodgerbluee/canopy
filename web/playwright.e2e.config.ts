import { defineConfig, devices } from '@playwright/test';

/** T07 auth e2e: real Go API + real owner against the built dev bundle.
 * global-setup.mjs provisions a disposable schema, bootstraps the owner via
 * the admin CLI and starts the API; serve-dist proxies /api to it. */
export default defineConfig({
  testDir: './e2e',
  testMatch: ['auth.spec.ts', 'collection.spec.ts', 'journal.spec.ts', 'purchases.spec.ts', 'capture.spec.ts', 'today.spec.ts', 'insights.spec.ts'],
  outputDir: '.pw-output-e2e',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 30_000,
  reporter: [
    ['list'],
    ['html', { open: 'never', outputFolder: '../.artifacts/agents/T07/playwright-report' }],
  ],
  globalSetup: './e2e/global-setup.mjs',
  use: {
    baseURL: 'http://127.0.0.1:4300',
    trace: 'off',
    screenshot: 'off',
    locale: 'en-US',
    timezoneId: 'UTC',
    colorScheme: 'no-preference',
  },
  webServer: {
    command: 'node ../scripts/serve-dist.mjs',
    port: 4300,
    reuseExistingServer: false,
    timeout: 30_000,
    env: { API_TARGET: 'http://127.0.0.1:8090' },
  },
  projects: [
    {
      name: 'desktop-1440',
      testMatch: ['auth.spec.ts', 'collection.spec.ts', 'journal.spec.ts', 'purchases.spec.ts'],
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 1000 } },
    },
    {
      // care seeds a profile, a plant and care occurrences: it must run after
      // the specs that assert empty-state (collection's starter prompt, plant
      // counts), so it lives in a dependent project.
      name: 'care-desktop',
      testMatch: 'care.spec.ts',
      dependencies: ['desktop-1440'],
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 1000 } },
    },
    {
      // capture creates plants and a purchase: it must run after the specs
      // that assert empty-state, so it lives in a dependent project
      name: 'capture-desktop',
      testMatch: 'capture.spec.ts',
      dependencies: ['desktop-1440'],
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 1000 } },
    },
    {
      // today seeds its own plant + plan and completes occurrences: it must
      // run after the empty-state specs, like the other seeding projects.
      name: 'today-desktop',
      testMatch: 'today.spec.ts',
      dependencies: ['capture-desktop'],
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 1000 } },
    },
    {
      // T34: seeds a profile, plant, plan and analysis runs, and leaves
      // open suggestions behind — must run after today.spec, which asserts
      // the suggestions slot is empty.
      name: 'insights-desktop',
      testMatch: 'insights.spec.ts',
      dependencies: ['today-desktop'],
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 1000 } },
    },
    {
      name: 'mobile-390',
      testMatch: 'auth.spec.ts', // collection journey is stateful; runs once on desktop
      dependencies: ['capture-desktop'],
      use: { ...devices['Desktop Chrome'], viewport: { width: 390, height: 844 } },
    },
  ],
});
