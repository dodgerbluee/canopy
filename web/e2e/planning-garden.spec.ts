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
test('acquire once, keep future work open, plant later and finish planning separately', async ({
  page,
}, info) => {
  const name = `Browser garden ${info.project.name} ${Date.now()}`;
  let plantId = '';
  let planId = '';
  try {
    await page.goto('/planning');
    await page.getByRole('button', { name: 'Plan a planting', exact: true }).click();
    const editor = page.getByRole('dialog', { name: 'Plan a planting', exact: true });
    await editor.getByLabel('What would you like to grow?').fill(name);
    const created = page.waitForResponse(
      (r) =>
        r.url().includes('/api/graphql') &&
        r.request().postData()?.includes('mutation CreatePlantingPlan') === true,
    );
    await editor.getByRole('button', { name: 'Save plan', exact: true }).click();
    planId = (await (await created).json()).data.createPlantingPlan.plan.id;
    await expect(editor).toBeHidden();
    const row = page.locator('.planning-row').filter({ hasText: name });
    await row.getByRole('button', { name, exact: false }).first().click();
    const detail = page.getByRole('dialog', { name, exact: true });
    await detail.getByRole('tab', { name: 'Actions', exact: true }).click();
    await detail.getByRole('button', { name: 'Add action', exact: true }).click();
    const action = page.getByRole('dialog', { name: 'Add a planned action', exact: true });
    await action.getByLabel('What is the next step?').selectOption('PLANT_OUT');
    await action.getByLabel('Action title').fill('Plant this living batch');
    await action.getByLabel('Planned day').fill('2027-03-01');
    await action.getByRole('button', { name: 'Add action', exact: true }).click();
    await expect(action).toBeHidden();
    await detail.getByRole('tab', { name: 'The plan', exact: true }).click();
    await detail.getByRole('button', { name: 'Add to garden', exact: true }).click();
    const garden = page.getByRole('dialog', { name: 'Add to garden', exact: true });
    await expect(garden.locator('[role=alert]')).toHaveCount(0);
    await expect(garden.getByLabel('Actual count')).toHaveValue('');
    await garden.getByLabel('Track as').selectOption('GROUP');
    expect(
      (await new AxeBuilder({ page }).include('.dialog-panel').analyze()).violations.map(
        (v) => v.id,
      ),
    ).toEqual([]);
    mkdirSync('../.artifacts/agents/PLAN07/screenshots', { recursive: true });
    await page.screenshot({
      path: `../.artifacts/agents/PLAN07/screenshots/garden-${info.project.name}.png`,
      fullPage: true,
    });
    const saved = page.waitForResponse(
      (r) =>
        r.url().includes('/api/graphql') &&
        r.request().postData()?.includes('mutation AddPlantingToGarden') === true,
    );
    await garden.getByRole('button', { name: 'Add to garden', exact: true }).click();
    const body = await (await saved).json();
    expect(body.data.addPlantingToGarden.problems).toEqual([]);
    plantId = body.data.addPlantingToGarden.plan.fulfillment.plant.id;
    await expect(garden).toBeHidden();
    await expect(detail.getByText('In your garden', { exact: true })).toBeVisible();
    await expect(detail.getByRole('button', { name: 'Add to garden', exact: true })).toHaveCount(0);
    await detail.getByRole('tab', { name: 'Actions', exact: true }).click();
    await expect(
      detail.locator('.planning-action-row').filter({ hasText: 'Plant this living batch' }),
    ).toContainText('Planned');
    await detail
      .getByRole('button', { name: 'Complete action Plant this living batch', exact: true })
      .click();
    const complete = page.getByRole('dialog', { name: 'Complete planned action', exact: true });
    await expect(
      complete.getByRole('button', { name: 'Mark complete', exact: true }),
    ).toBeEnabled();
    await complete.getByLabel('Record the actual placement at planting').check();
    await complete.getByLabel('Actual placement', { exact: true }).selectOption('GROUND');
    await complete.getByRole('button', { name: 'Mark complete', exact: true }).click();
    await expect(complete).toBeHidden();
    await expect(detail.getByText(/Completed /)).toBeVisible();
    await detail.getByRole('tab', { name: 'The plan', exact: true }).click();
    await expect(detail.getByText('In progress', { exact: true })).toBeVisible();
    await detail.getByRole('button', { name: 'Mark complete', exact: true }).click();
    const finished = page.waitForResponse(
      (r) =>
        r.url().includes('/api/graphql') &&
        r.request().postData()?.includes('mutation ChangePlantingPlanState') === true,
    );
    await page
      .getByRole('dialog', { name: 'Mark this plan complete?', exact: true })
      .getByRole('button', { name: 'Confirm', exact: true })
      .click();
    expect((await (await finished).json()).data.changePlantingPlanState.plan.state).toBe(
      'COMPLETED',
    );
    await expect(detail).toBeHidden();
    await expect(row).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(
      true,
    );
  } finally {
    const session = await (await page.request.get('/api/auth/session')).json();
    const headers = { 'X-CSRF-Token': session.csrfToken };
    if (plantId) {
      const result = await (
        await page.request.post('/api/graphql', {
          headers,
          data: {
            query: 'query($id:ID!){plant(id:$id){id name revision}}',
            variables: { id: plantId },
          },
        })
      ).json();
      const plant = result.data?.plant;
      expect(plant?.name).toBe(name);
      const purge = await (
        await page.request.post('/api/graphql', {
          headers,
          data: {
            query: 'mutation($input:PurgePlantInput!){purgePlant(input:$input){problems{message}}}',
            variables: {
              input: {
                id: plantId,
                expectedRevision: plant.revision,
                confirm: 'PERMANENTLY_DELETE_PLANT',
              },
            },
          },
        })
      ).json();
      expect(purge.data.purgePlant.problems).toEqual([]);
    }
    if (planId) {
      const data = await (
        await page.request.post('/api/graphql', {
          headers,
          data: {
            query: 'query($id:ID!){plantingPlan(id:$id){id name state revision}}',
            variables: { id: planId },
          },
        })
      ).json();
      const plan = data.data.plantingPlan;
      expect(plan.name).toBe(name);
      if (['IDEA', 'PLANNED', 'IN_PROGRESS'].includes(plan.state)) {
        const cancelled = await (
          await page.request.post('/api/graphql', {
            headers,
            data: {
              query:
                'mutation($input:CancelPlantingPlanInput!){cancelPlantingPlan(input:$input){problems{message}}}',
              variables: { input: { id: planId, expectedRevision: plan.revision } },
            },
          })
        ).json();
        expect(cancelled.data.cancelPlantingPlan.problems).toEqual([]);
      }
    }
  }
});
