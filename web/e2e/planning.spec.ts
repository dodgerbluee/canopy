import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { mkdirSync } from 'node:fs';

let cookies: Awaited<ReturnType<import('@playwright/test').BrowserContext['cookies']>> | null =
  null;
test.beforeEach(async ({ page }) => {
  if (cookies) await page.context().addCookies(cookies);
  else {
    const response = await page.request.post('/api/auth/login', {
      data: { username: 'planner-preview', password: 'canopy-preview-2027' },
    });
    expect(response.status()).toBe(200);
    cookies = await page.context().cookies();
  }
  await page.goto('/planning');
  await expect(page.getByRole('heading', { name: /Your next growing season/ })).toBeVisible();
});

test('planning overview, hidden filters, source distinctions and themes', async ({
  page,
}, info) => {
  await page.goto('/planning');
  await expect(page.getByRole('heading', { name: /Your next growing season/ })).toBeVisible();
  await expect(
    page.getByRole('button', { name: 'Another fig tree', exact: false }).first(),
  ).toBeVisible();
  await expect(page.locator('#planning-search')).toHaveCount(0);
  await page.getByRole('button', { name: /^Filters/ }).click();
  const dialog = page.getByRole('dialog', { name: 'Find your plans', exact: true });
  await dialog.getByLabel('Plant, cultivar or scientific name').fill('Another fig');
  await dialog.getByRole('button', { name: 'Apply filters' }).click();
  await expect(page).toHaveURL(/q=Another/);
  await expect(page.locator('.planning-row')).toHaveCount(1);
  await page.getByRole('button', { name: 'Clear filters', exact: true }).click();
  await expect(page.locator('.planning-row')).toHaveCount(3);
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1),
  ).toBe(true);
  const result = await new AxeBuilder({ page }).analyze();
  expect(result.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) }))).toEqual(
    [],
  );
  mkdirSync('../.artifacts/agents/PLAN05/screenshots', { recursive: true });
  await page.screenshot({
    path: `../.artifacts/agents/PLAN05/screenshots/${info.project.name}.png`,
    fullPage: true,
  });
});

test('pristine idea, full replacement edit, exact free estimate and protected drafts', async ({
  page,
}, info) => {
  const name = `Browser idea ${info.project.name} ${Date.now()}`;
  await page.goto('/planning?view=wishlist');
  await page.getByRole('button', { name: 'Add an idea', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Keep an idea', exact: true });
  await expect(dialog.locator('.field-error')).toHaveCount(0);
  await dialog.getByLabel('What would you like to grow?').fill(name);
  await dialog.getByRole('button', { name: 'Save idea', exact: true }).click();
  await expect(dialog).toBeHidden();
  const row = page.locator('.planning-row').filter({ hasText: name });
  await expect(row).toBeVisible();
  await row.getByRole('button', { name: `Edit ${name}`, exact: true }).click();
  const editor = page.getByRole('dialog', { name: 'Edit planting plan', exact: true });
  await editor.getByLabel('What would you like to grow?').fill(name + ' draft');
  await editor.getByRole('button', { name: 'Cancel', exact: true }).click();
  const discard = page.getByRole('dialog', { name: 'Discard changes?', exact: true });
  await expect(discard).toBeVisible();
  await discard.getByRole('button', { name: 'Keep editing' }).click();
  await expect(editor.getByLabel('What would you like to grow?')).toHaveValue(name + ' draft');
  await editor.getByLabel('What would you like to grow?').fill(name);
  await editor.locator('summary').filter({ hasText: 'Budget & notes' }).click();
  await editor.getByLabel('Budget estimate').fill('0');
  await editor.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(editor).toBeHidden();
  await row.getByRole('button', { name: name, exact: false }).first().click();
  const detail = page.getByRole('dialog', { name: name, exact: true });
  await expect(detail.getByText('0.00 USD', { exact: true })).toBeVisible();
  await detail.getByRole('button', { name: 'Edit this plan', exact: true }).click();
  await editor.locator('summary').filter({ hasText: 'Plan options' }).click();
  await editor.getByRole('button', { name: 'Cancel this plan', exact: true }).click();
  const cancel = page.getByRole('dialog', { name: 'Cancel this plan?', exact: true });
  await cancel.getByRole('button', { name: 'Confirm', exact: true }).click();
  await expect(row).toBeHidden();
});

test('explicit planned action can be added, edited and removed', async ({ page }, info) => {
  const name = `Browser action ${info.project.name} ${Date.now()}`;
  await page.goto('/planning');
  await page.getByRole('button', { name: 'Plan a planting', exact: true }).click();
  const editor = page.getByRole('dialog', { name: 'Plan a planting', exact: true });
  await editor.getByLabel('What would you like to grow?').fill(name);
  await editor.getByRole('button', { name: 'Save plan', exact: true }).click();
  await expect(editor).toBeHidden();
  const row = page.locator('.planning-row').filter({ hasText: name });
  await row.getByRole('button', { name: name, exact: false }).first().click();
  const detail = page.getByRole('dialog', { name: name, exact: true });
  await detail.getByRole('tab', { name: 'Actions', exact: true }).click();
  await detail.getByRole('button', { name: 'Add action', exact: true }).click();
  const action = page.getByRole('dialog', { name: 'Add a planned action', exact: true });
  await action.getByLabel('Action title').fill('Prepare a test container');
  await action.getByLabel('Planned day').fill('2027-03-14');
  await action.getByRole('button', { name: 'Add action', exact: true }).click();
  await expect(action).toBeHidden();
  await expect(detail.getByText('Prepare a test container', { exact: true })).toBeVisible();
  await detail
    .getByRole('button', { name: 'Edit action Prepare a test container', exact: true })
    .click();
  const edit = page.getByRole('dialog', { name: 'Edit planned action', exact: true });
  await edit.getByRole('button', { name: 'Remove this action', exact: true }).click();
  await edit.getByRole('button', { name: 'Remove action', exact: true }).click();
  await expect(edit).toBeHidden();
  await expect(detail.getByText('Prepare a test container', { exact: true })).toBeHidden();
  await detail.getByRole('button', { name: 'Edit this plan', exact: true }).click();
  const planEdit = page.getByRole('dialog', { name: 'Edit planting plan', exact: true });
  await planEdit.locator('summary').filter({ hasText: 'Plan options' }).click();
  await planEdit.getByRole('button', { name: 'Cancel this plan', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Cancel this plan?', exact: true })
    .getByRole('button', { name: 'Confirm', exact: true })
    .click();
});

test('source selection keeps its precision and creates no implicit action', async ({ page }) => {
  await page.goto('/planning');
  const row = page.locator('.planning-row').filter({ hasText: 'Another fig tree' });
  await row.getByRole('button', { name: 'Another fig tree', exact: false }).first().click();
  const detail = page.getByRole('dialog', { name: 'Another fig tree', exact: true });
  await detail.getByRole('tab', { name: 'Windows', exact: true }).click();
  await detail.getByRole('button', { name: 'Review & choose', exact: true }).first().click();
  const chooser = page.getByRole('dialog', { name: 'Review a growing window', exact: true });
  await expect(chooser.getByRole('link').first()).toHaveAttribute('href', /^https:\/\//);
  expect(
    (await new AxeBuilder({ page }).include('.dialog-panel').analyze()).violations.map((v) => v.id),
  ).toEqual([]);
  const reply = page.waitForResponse(
    (response) =>
      response.url().includes('/api/graphql') &&
      response.request().postData()?.includes('mutation ConfirmPlantingSchedule') === true,
  );
  await chooser.getByRole('button', { name: 'Choose this window', exact: true }).click();
  const response = await (await reply).json();
  const plan = response.data.confirmPlantingSchedule.plan;
  expect(response.data.confirmPlantingSchedule.problems).toEqual([]);
  expect(plan.targetPeriod.precision).toBe(plan.acceptedSchedule.supportedPeriod.precision);
  if (plan.targetPeriod.precision !== 'DATE_RANGE') expect(plan.targetPeriod.dates).toBeNull();
  expect(plan.actions).toHaveLength(1);
  await expect(chooser).toBeHidden();
});

test('frost-relative preview retains the explicit assumption on confirmation', async ({ page }) => {
  await page.goto('/planning');
  const row = page.locator('.planning-row').filter({ hasText: 'Summer tomatoes' });
  await row.getByRole('button', { name: 'Summer tomatoes', exact: false }).first().click();
  const detail = page.getByRole('dialog', { name: 'Summer tomatoes', exact: true });
  await detail.getByRole('tab', { name: 'Windows', exact: true }).click();
  await detail
    .getByRole('button', { name: /^(View source & conditions|Review & choose)$/, exact: true })
    .first()
    .click();
  const chooser = page.getByRole('dialog', { name: 'Review a growing window', exact: true });
  await chooser.getByLabel('Assumed first frost date').fill('2027-11-15');
  await chooser
    .getByRole('button', { name: 'Preview with these assumptions', exact: true })
    .click();
  await expect(
    chooser.getByRole('button', { name: 'Choose this window', exact: true }),
  ).toBeEnabled();
  const reply = page.waitForResponse(
    (response) =>
      response.url().includes('/api/graphql') &&
      response.request().postData()?.includes('mutation ConfirmPlantingSchedule') === true,
  );
  await chooser.getByRole('button', { name: 'Choose this window', exact: true }).click();
  const response = await (await reply).json();
  expect(response.data.confirmPlantingSchedule.problems).toEqual([]);
  expect(response.data.confirmPlantingSchedule.plan.acceptedSchedule.assumptions.anchors).toEqual([
    { kind: 'FIRST_FROST', on: '2027-11-15' },
  ]);
  expect(response.data.confirmPlantingSchedule.plan.actions).toEqual([]);
  await expect(chooser).toBeHidden();
});
