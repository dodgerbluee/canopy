import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { mkdirSync } from 'node:fs';
let cookies: Awaited<ReturnType<import('@playwright/test').BrowserContext['cookies']>> | null =
  null;
test.beforeEach(async ({ page }) => {
  if (cookies) await page.context().addCookies(cookies);
  else {
    expect(
      (
        await page.request.post('/api/auth/login', {
          data: { username: 'planner-preview', password: 'canopy-preview-2027' },
        })
      ).ok(),
    ).toBe(true);
    cookies = await page.context().cookies();
  }
});
test('local Garden model returns a cited note, which stays unsaved until normal confirmation', async ({
  page,
}, info) => {
  test.setTimeout(200000);
  const session = await (await page.request.get('/api/auth/session')).json();
  const headers = { 'X-CSRF-Token': session.csrfToken };
  // Restrict this live model check to the known local endpoint and generated demo data.
  const guard = await (
    await page.request.post('/api/graphql', {
      headers,
      data: {
        query:
          'query{plants(first:1){items{id}} aiConnections{bindings{feature mode connectionId} connections{id endpoint deploymentLocal} deploymentDefaults{feature endpoint deploymentLocal}}}',
      },
    })
  ).json();
  expect(guard.data.plants.items).toEqual([]);
  const config = guard.data.aiConnections;
  const binding = config.bindings.find((b: { feature: string }) => b.feature === 'GARDEN');
  const chosen =
    binding?.mode === 'CONNECTION'
      ? config.connections.find((c: { id: string }) => c.id === binding.connectionId)
      : config.deploymentDefaults.find((d: { feature: string }) => d.feature === 'GARDEN');
  expect(chosen.deploymentLocal).toBe(true);
  expect(new URL(chosen.endpoint).host).toBe('192.168.69.130:8080');
  await page.goto('/planning');
  const loaded = page.waitForResponse(
    (r) =>
      r.url().includes('/api/graphql') &&
      r.request().postData()?.includes('query PlantingPlanDetail') === true,
  );
  await page
    .locator('.planning-row')
    .filter({ hasText: 'Another fig tree' })
    .getByRole('button', { name: 'Another fig tree', exact: false })
    .first()
    .click();
  const original = (await (await loaded).json()).data.plantingPlan;
  expect(original.notes).toBe('Compare compact varieties. Keep room for the canopy to grow.');
  const detail = page.getByRole('dialog', { name: 'Another fig tree', exact: true });
  await detail.getByRole('button', { name: 'Ask about this plan', exact: true }).click();
  const assistant = page.getByRole('dialog', { name: 'Help me plan', exact: true });
  await assistant
    .getByLabel('What would you like to work out?')
    .fill(
      'Suggest one concise draft note I can review for this fig plan, using its supplied sources. Keep my chosen dates and budget unchanged. Do not propose commands.',
    );
  const result = page.waitForResponse(
    (r) =>
      r.url().includes('/api/graphql') &&
      r.request().postData()?.includes('mutation AskPlantingPlanner') === true,
    { timeout: 160000 },
  );
  await assistant.getByRole('button', { name: 'Ask about these plans', exact: true }).click();
  const body = await (await result).json();
  expect(body.errors).toBeUndefined();
  const reply = body.data.askPlantingPlanner;
  expect(reply.isAI).toBe(true);
  expect(reply.sources.length).toBeGreaterThan(0);
  expect(reply.note.planId).toBe(original.id);
  await expect(assistant.getByText('GARDEN ASSISTANT', { exact: true })).toBeVisible();
  expect(
    (await new AxeBuilder({ page }).include('.dialog-panel').analyze()).violations.map((v) => v.id),
  ).toEqual([]);
  mkdirSync('../.artifacts/agents/PLAN08/screenshots', { recursive: true });
  await page.screenshot({
    path: `../.artifacts/agents/PLAN08/screenshots/assistant-${info.project.name}.png`,
    fullPage: true,
  });
  await assistant.getByRole('button', { name: 'Review note in plan', exact: true }).click();
  const editor = page.getByRole('dialog', { name: 'Edit planting plan', exact: true });
  await expect(editor).toBeVisible();
  expect(await editor.getByLabel('Notes', { exact: true }).inputValue()).toContain(original.notes);
  expect(await editor.getByLabel('Notes', { exact: true }).inputValue()).toContain(reply.note.text);
  // Applying a draft opens an editor; verify it did not issue a hidden save.
  const verify = await (
    await page.request.post('/api/graphql', {
      headers,
      data: {
        query: 'query($id:ID!){plantingPlan(id:$id){revision notes actions{id}}}',
        variables: { id: original.id },
      },
    })
  ).json();
  expect(verify.data.plantingPlan.revision).toBe(original.revision);
  expect(verify.data.plantingPlan.notes).toBe(original.notes);
  expect(verify.data.plantingPlan.actions).toHaveLength(original.actions.length);
  await editor.getByRole('button', { name: 'Cancel', exact: true }).click();
  await page
    .getByRole('dialog', { name: 'Discard changes?', exact: true })
    .getByRole('button', { name: 'Discard changes', exact: true })
    .click();
  await expect(editor).toBeHidden();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
    true,
  );
});
test('unsupported ideas receive an honest manual-planning explanation', async ({ page }) => {
  await page.goto('/planning?view=wishlist');
  await page
    .locator('.planning-row')
    .filter({ hasText: 'Blueberries by the patio' })
    .getByRole('button', { name: 'Blueberries by the patio', exact: false })
    .first()
    .click();
  await page
    .getByRole('dialog', { name: 'Blueberries by the patio', exact: true })
    .getByRole('button', { name: 'Ask about this plan', exact: true })
    .click();
  const assistant = page.getByRole('dialog', { name: 'Help me plan', exact: true });
  await assistant.getByRole('button', { name: 'Ask about these plans', exact: true }).click();
  await expect(assistant.getByText('MANUAL PLANNING', { exact: true })).toBeVisible();
  await expect(
    assistant.getByRole('button', { name: 'Review note in plan', exact: true }),
  ).toHaveCount(0);
});
