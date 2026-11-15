import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { mkdirSync } from 'node:fs';

// Browser states use controlled rain responses. Real worker/provider fixtures,
// atomic recording and cross-workspace boundaries run separately against PG17.
type Review = {
  status: string;
  areaLabel: string;
  checkedAt: string | null;
  proposals: { id: string; day: string; estimatedAmountMm: string; checkedAt: string }[];
};
const ready = (): Review => ({
  status: 'READY',
  areaLabel: 'Your garden',
  checkedAt: new Date().toISOString(),
  proposals: [
    {
      id: 'browser-rain',
      day: '2026-11-15',
      estimatedAmountMm: '8',
      checkedAt: new Date().toISOString(),
    },
  ],
});
async function signIn(page: Page) {
  const res = await page.request.post('/api/auth/login', {
    data: { username: 'planner-preview', password: 'canopy-preview-2027' },
  });
  expect(res.status()).toBe(200);
}
async function stubReview(page: Page, initial = ready(), failFirst = false) {
  let review = initial;
  const records: Record<string, unknown>[] = [];
  const dismissals: Record<string, unknown>[] = [];
  await page.route('**/api/graphql', async (route) => {
    const body = route.request().postDataJSON();
    const query = String(body.query ?? '');
    if (query.includes('query TodayOverview')) {
      const res = await route.fetch();
      const json = await res.json();
      expect(json.errors).toBeUndefined();
      json.data.todayOverview.rainReview = review;
      await route.fulfill({ response: res, json });
      return;
    }
    if (query.includes('mutation RecordRain')) {
      records.push(body.variables);
      if (failFirst && records.length === 1) {
        await route.abort('failed');
        return;
      }
      review = { ...review, proposals: [] };
      const input = body.variables.input;
      await route.fulfill({
        json: {
          data: {
            recordRain: {
              rain: {
                id: 'browser-rain-event',
                day: input.day,
                amountMm: input.amountMm,
                note: null,
                revision: 1,
              },
              problems: [],
              clientMutationId: body.variables.clientMutationId,
            },
          },
        },
      });
      return;
    }
    if (query.includes('mutation DismissRainReview')) {
      dismissals.push(body.variables);
      review = { ...review, proposals: [] };
      await route.fulfill({
        json: {
          data: {
            dismissRainReview: {
              dismissedId: 'browser-rain',
              problems: [],
              clientMutationId: body.variables.clientMutationId,
            },
          },
        },
      });
      return;
    }
    await route.continue();
  });
  return { records, dismissals };
}

test('sign-in has no default owner username', async ({ page }) => {
  await page.goto('/login');
  await expect(page.getByLabel('Username', { exact: true })).toHaveValue('');
});

test('live dashboard reads the running rain monitor', async ({ page }) => {
  await signIn(page);
  await page.goto('/');
  await expect(
    page.getByRole('heading', { name: /^(Rain watch|Did your garden get rain\?)$/ }),
  ).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Today failed to load', exact: true }),
  ).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Add rain manually', exact: true })).toBeVisible();
});

test('unknown amount, safe retry and reviewed save', async ({ page }) => {
  await signIn(page);
  const { records } = await stubReview(page, ready(), true);
  await page.goto('/');
  await page.getByRole('button', { name: 'Yes, it rained', exact: true }).click();
  const modal = page.getByRole('dialog', { name: 'Record rain', exact: true });
  await expect(modal.getByLabel('How much?', { exact: false })).toHaveValue('');
  await modal.getByRole('button', { name: 'Save rain', exact: true }).click();
  await expect(modal.getByRole('alert')).toContainText('retrying is safe');
  await modal.getByRole('button', { name: 'Save rain', exact: true }).click();
  await expect(modal).toBeHidden();
  expect(records).toHaveLength(2);
  expect(records[0]).toEqual(records[1]);
  expect(records[0].input).toEqual({
    day: '2026-11-15',
    amountMm: null,
    rainReviewId: 'browser-rain',
  });
  await expect(page.getByRole('heading', { name: 'Rain watch', exact: true })).toBeVisible();
});

test('entered depth and explicit rejection stay separate', async ({ page }) => {
  await signIn(page);
  const { records, dismissals } = await stubReview(page);
  await page.goto('/');
  await page.getByRole('button', { name: 'Yes, it rained', exact: true }).click();
  const modal = page.getByRole('dialog', { name: 'Record rain', exact: true });
  await modal.getByLabel('How much?', { exact: false }).fill('-1');
  await modal.getByRole('button', { name: 'Save rain', exact: true }).click();
  await expect(modal.getByRole('alert')).toBeVisible();
  expect(records).toHaveLength(0);
  await modal.getByLabel('How much?', { exact: false }).fill('0.5');
  await modal.getByRole('button', { name: 'Save rain', exact: true }).click();
  await expect(modal).toBeHidden();
  expect(records[0].input).toEqual({
    day: '2026-11-15',
    amountMm: '12.7',
    rainReviewId: 'browser-rain',
  });
  expect(dismissals).toHaveLength(0);
});

test('rain prompt is accessible and fits desktop and phone', async ({ page }, info) => {
  await signIn(page);
  const { records, dismissals } = await stubReview(page);
  await page.goto('/');
  await expect(
    page.getByRole('heading', { name: 'Did your garden get rain?', exact: true }),
  ).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
    true,
  );
  const axe = await new AxeBuilder({ page }).analyze();
  expect(axe.violations.map((v) => ({ id: v.id, targets: v.nodes.map((n) => n.target) }))).toEqual(
    [],
  );
  mkdirSync('../.artifacts/agents/FIX09/screenshots', { recursive: true });
  await page.screenshot({
    path: `../.artifacts/agents/FIX09/screenshots/${info.project.name}.png`,
    fullPage: true,
  });
  await page.getByRole('button', { name: 'No rain here', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Rain watch', exact: true })).toBeVisible();
  expect(records).toHaveLength(0);
  expect(dismissals).toHaveLength(1);
});
