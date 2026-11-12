import { defineConfig, devices } from '@playwright/test';

/** Visual/state-contract suite (T05): fixture screens at three viewports and
 * both themes. Screens + axe results land in .artifacts/agents/T05/. */
export default defineConfig({
  testDir: './e2e',
  testMatch: 'visual.spec.ts',
  outputDir: '.pw-output',
  fullyParallel: true,
  retries: 0,
  reporter: [
    ['list'],
    ['html', { open: 'never', outputFolder: '../.artifacts/agents/T05/playwright-report' }],
  ],
  use: {
    baseURL: 'http://127.0.0.1:4300',
    trace: 'off',
    screenshot: 'off',
    // deterministic rendering: fixed locale/time so fixture dates never shift
    locale: 'en-US',
    timezoneId: 'UTC',
    colorScheme: 'no-preference',
  },
  webServer: {
    command: 'node ../scripts/serve-dist.mjs',
    port: 4300,
    reuseExistingServer: false,
    timeout: 30_000,
  },
  projects: [
    {
      name: 'mobile-390',
      use: { ...devices['Desktop Chrome'], viewport: { width: 390, height: 844 } },
    },
    {
      name: 'tablet-768',
      use: { ...devices['Desktop Chrome'], viewport: { width: 768, height: 1024 } },
    },
    {
      name: 'desktop-1440',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 1000 } },
    },
  ],
});
