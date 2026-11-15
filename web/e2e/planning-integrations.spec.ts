import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
let cookies: Awaited<ReturnType<import('@playwright/test').BrowserContext['cookies']>> | null =
  null;
test.beforeEach(async ({ page }) => {
  if (cookies) await page.context().addCookies(cookies);
  else {
    const r = await page.request.post('/api/auth/login', {
      data: { username: 'planner-preview', password: 'canopy-preview-2027' },
    });
    expect(r.ok()).toBe(true);
    cookies = await page.context().cookies();
  }
});
test('chosen action is separate in Calendar and Today and opens its own plan', async ({
  page,
}, info) => {
  const title = `Integration action ${info.project.name} ${Date.now()}`;
  await page.goto('/planning');
  await page
    .locator('.planning-row')
    .filter({ hasText: 'Another fig tree' })
    .getByRole('button', { name: 'Another fig tree', exact: false })
    .first()
    .click();
  const detail = page.getByRole('dialog', { name: 'Another fig tree', exact: true });
  await detail.getByRole('tab', { name: 'Actions', exact: true }).click();
  await detail.getByRole('button', { name: 'Add action', exact: true }).click();
  const editor = page.getByRole('dialog', { name: 'Add a planned action', exact: true });
  const day = await page.evaluate(() =>
    new Intl.DateTimeFormat('en-CA', {
      timeZone: 'America/Chicago',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).format(new Date()),
  );
  await editor.getByLabel('Action title').fill(title);
  await editor.getByLabel('Planned day').fill(day);
  await expect(editor.getByLabel('Remind me in Inbox')).not.toBeChecked();
  await editor.getByRole('button', { name: 'Add action', exact: true }).click();
  await expect(editor).toBeHidden();
  await page.goto('/calendar');
  const planned = page.locator('.planning-entry').filter({ hasText: title });
  await expect(planned).toBeVisible();
  await expect(page.locator('.month-meta')).toContainText('0 recorded entries');
  const mobile = page.getByRole('button', { name: /Plants & filters/ });
  if (await mobile.isVisible()) await mobile.click();
  const filter = page.getByLabel('Chosen action days').filter({ visible: true });
  await filter.uncheck();
  if (await mobile.isVisible())
    await page
      .getByRole('dialog', { name: 'Plants & filters', exact: true })
      .getByRole('button', { name: 'Done', exact: true })
      .click();
  await expect(planned).toBeHidden();
  await page.goto('/');
  await expect(page.locator('upcoming-plantings').getByText(title, { exact: true })).toBeVisible();
  await page.locator('upcoming-plantings').getByRole('link').filter({ hasText: title }).click();
  await expect(page).toHaveURL(/planning\?plan=/);
  await expect(detail).toBeVisible();
  await expect(detail.getByText(title, { exact: true })).toBeVisible();
  await detail.getByRole('button', { name: `Edit action ${title}`, exact: true }).click();
  const edit = page.getByRole('dialog', { name: 'Edit planned action', exact: true });
  await edit.getByRole('button', { name: 'Remove this action', exact: true }).click();
  await edit.getByRole('button', { name: 'Remove action', exact: true }).click();
  await expect(edit).toBeHidden();
});
test('Garden year overlay is optional and does not count plans as owned plants', async ({
  page,
}) => {
  await page.goto('/year');
  await expect(page.getByRole('heading', { name: /A year of growing/ })).toBeVisible();
  const toggle = page.getByLabel('Include planned plantings');
  await expect(toggle).not.toBeChecked();
  const year = new Date().getFullYear();
  for (let y = year; y < 2027; y++)
    await page.getByRole('button', { name: 'Next year', exact: true }).click();
  await toggle.check();
  await expect(
    page.locator('.planned-year-row').filter({ hasText: 'Another fig tree' }),
  ).toBeVisible();
  await expect(page.locator('.year-overview')).toContainText('0 plants');
  expect(
    (await new AxeBuilder({ page }).include('year-plantings').analyze()).violations.map(
      (v) => v.id,
    ),
  ).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
    true,
  );
  await toggle.uncheck();
  await expect(page.locator('.planned-year-row')).toHaveCount(0);
});
test('Inbox links planning notifications without marking them read on open', async ({ page }) => {
  await page.goto('/inbox');
  const notice = page.locator('.notice-row').filter({ hasText: 'Preview reminder delivery check' });
  await expect(notice).toBeVisible();
  await expect(notice.getByRole('button', { name: 'Mark as read', exact: true })).toBeVisible();
  await notice.getByRole('link', { name: 'Review planting plan', exact: true }).click();
  await expect(page).toHaveURL(/\/planning$/);
});
