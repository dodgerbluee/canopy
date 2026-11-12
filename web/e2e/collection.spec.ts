import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

/** T10 acceptance journey: opt-in starter set (five trees, two groups,
 * counts unknown) → keyboard-driven add at mobile size → deep-link detail
 * → edit → archive → archived filter → restore → live overview snapshot —
 * all against the real Go API. Runs on the desktop project only (single
 * shared disposable schema; the journey is stateful by design). */

const PASSPHRASE = process.env.E2E_PASSPHRASE ?? 'e2e-dev-passphrase';

async function signIn(page: import('@playwright/test').Page): Promise<void> {
  await page.goto('/');
  await page.getByLabel('Password').fill(PASSPHRASE);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(
    page.getByRole('heading', { level: 1, name: 'Your garden, at a glance.' }),
  ).toBeVisible();
}

test('starter opt-in, keyboard-driven add at mobile size, deep link, edit, archive, restore', async ({
  page,
}) => {
  await signIn(page);

  // ── empty garden: nothing is seeded without an explicit choice ─────────
  await expect(
    page.getByRole('heading', { name: 'Start with a curated collection?' }),
  ).toBeVisible();

  // ── starter opt-in: five trees + two groups, counts unknown ────────────
  await page.getByRole('button', { name: 'Opt in to starter collection' }).click();
  await expect(page.getByText('Starter collection added')).toBeVisible();
  await expect(page).toHaveURL(/\/plants$/);
  await expect(page.getByText('7 shown')).toBeVisible();
  await expect(page.getByText('Alpine strawberries')).toBeVisible();
  await expect(page.getByText('Thornless blackberries')).toBeVisible();
  await expect(page.getByText('count unknown').first()).toBeVisible(); // groups stay honest

  // ── mobile viewport, keyboard-only add ─────────────────────────────────
  await page.setViewportSize({ width: 390, height: 844 });
  await page
    .getByRole('navigation', { name: 'Primary mobile' })
    .getByRole('link', { name: 'Plants' })
    .click();
  await expect(page).toHaveURL(/\/plants$/);
  await expect(page.getByText('7 shown')).toBeVisible();

  await page.getByRole('link', { name: 'Add Plant' }).click();
  await expect(page.getByRole('dialog', { name: 'Add a plant', exact: true })).toBeVisible();

  // Dirty cancellation and browser Back preserve the draft until explicit discard.
  const entry = page.getByRole('dialog', { name: 'Add a plant', exact: true });
  await entry.getByLabel('Name', { exact: true }).fill('Unsaved draft');
  await entry.getByRole('button', { name: 'Cancel', exact: true }).click();
  const discard = page.getByRole('dialog', {
    name: 'Discard changes?',
    exact: true,
  });
  await expect(discard).toBeVisible();
  await new AxeBuilder({ page }).analyze().then((r) => expect(r.violations).toEqual([]));
  await discard.getByRole('button', { name: 'Keep editing', exact: true }).click();
  await expect(entry.getByLabel('Name', { exact: true })).toHaveValue('Unsaved draft');
  await page.goBack();
  await expect(discard).toBeVisible();
  await discard.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await expect(page).toHaveURL(/\/plants$/);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.getByRole('link', { name: 'Add Plant', exact: true }).click();
  await expect(entry.getByLabel('Name', { exact: true })).toHaveValue('');

  // Keyboard: type name, leave optional identity fields blank, then Enter submits.
  await page.getByLabel('Name', { exact: true }).pressSequentially('Meyer lemon', { delay: 15 });
  await page.keyboard.press('Tab'); // cultivar — deliberately left blank
  await page.keyboard.press('Tab'); // scientific name
  await page.keyboard.press('Enter');

  await expect(page).toHaveURL(/\/plants\/[0-9a-f-]{36}$/);
  const detailUrl = page.url();
  await expect(page.getByRole('heading', { level: 1, name: /Meyer lemon/ })).toBeVisible();
  // unknown cultivar is stated, never guessed:
  await expect(page.getByText('Cultivar unknown')).toBeVisible();

  // ── deep link survives reload ──────────────────────────────────────────
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.reload();
  await expect(page.getByRole('heading', { level: 1, name: /Meyer lemon/ })).toBeVisible();

  // ── edit via deep link ─────────────────────────────────────────────────
  await page.goto(detailUrl + '/edit');
  await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Meyer lemon');
  await page.getByLabel('Cultivar').fill('Meyer');
  await page.getByRole('button', { name: 'Save changes' }).click();
  await expect(page.getByText('Plant updated')).toBeVisible();
  await expect(page.getByRole('heading', { level: 1, name: /Meyer lemon/ })).toBeVisible();
  await expect(page.getByText('Cultivar unknown')).toHaveCount(0);

  // ── archive → archived filter → restore ────────────────────────────────
  await page.getByRole('button', { name: 'Archive' }).click();
  await expect(page.getByText('Plant archived')).toBeVisible();
  await expect(page.getByText('Archived — hidden from the default collection view')).toBeVisible();

  await page.goto('/plants');
  await expect(page.getByText('7 shown')).toBeVisible();
  // Cards are native <article>-style rows whose main link takes its name from
  // the plant title, so the count is structural (one main link per card row).
  await expect(page.getByRole('list', { name: 'Plants' }).locator('li a.card')).toHaveCount(7);
  // quick filters live in the slide-out panel behind one Filter button
  await expect(page.getByLabel('Status', { exact: true })).toBeHidden();
  await page.getByRole('button', { name: 'Filter', exact: true }).click();
  await page.getByLabel('Status', { exact: true }).selectOption('archived');
  await page.getByRole('button', { name: 'Close filters' }).click();
  await expect(
    page.getByRole('list', { name: 'Plants' }).getByText('Archived', { exact: true }).first(),
  ).toBeVisible();
  // Restore lives behind the archived card's actions disclosure.
  const archivedCard = page
    .getByRole('list', { name: 'Plants' })
    .locator('li')
    .filter({ has: page.getByRole('heading', { name: 'Meyer lemon' }) });
  await archivedCard.locator('summary').click();
  await archivedCard.getByRole('button', { name: 'Restore' }).click();
  await expect(page.getByText('“Meyer lemon” restored')).toBeVisible();
  await page.getByRole('button', { name: 'Filter', exact: true }).click();
  // the drawer's status control is a select (owner's drawer redesign landed
  // in the favicon commit; this stale quick-chip click never matched it)
  await page.getByLabel('Status', { exact: true }).selectOption('active');
  await page.getByRole('button', { name: 'Close filters' }).click();
  await expect(page.getByText('8 shown')).toBeVisible();

  // ── today reflects the live garden (no starter card once non-empty) ────
  await page.goto('/');
  await expect(
    page.getByRole('heading', { level: 1, name: 'Your garden, at a glance.' }),
  ).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Nothing due today' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Opt in to starter collection' })).toHaveCount(0);

  // ── a11y on the three core screens ─────────────────────────────────────
  await page.goto('/plants');
  await expect(
    page.getByRole('heading', { level: 1, name: 'Every plant you keep.' }),
  ).toBeVisible();
  await new AxeBuilder({ page }).analyze().then((r) => expect(r.violations).toEqual([]));
  await page.goto(detailUrl);
  await expect(page.getByRole('heading', { level: 1, name: /Meyer lemon/ })).toBeVisible();
  await new AxeBuilder({ page }).analyze().then((r) => expect(r.violations).toEqual([]));
  await page.goto('/plants/new');
  await expect(page.getByRole('dialog', { name: 'Add a plant', exact: true })).toBeVisible();
  await new AxeBuilder({ page }).analyze().then((r) => expect(r.violations).toEqual([]));
});

/** Defect regression: the move form used (ngSubmit) without FormsModule, so
 * submit never fired — "Save move" was a native form submit (page reload,
 * move lost). Covers both submit paths: button click and Enter. */
test('move form submits for real: new location + pot via button, second move via Enter', async ({
  page,
}) => {
  await signIn(page);

  await page.goto('/plants/new');
  await page.getByLabel('Name', { exact: true }).fill('Basil');
  await page.getByRole('button', { name: 'Add plant' }).click();
  await expect(page).toHaveURL(/\/plants\/[0-9a-f-]{36}$/);
  await page.getByRole('tab', { name: 'Details' }).click();
  await expect(page.getByText('No location set yet.')).toBeVisible();

  // ── move #1 via the Save move button — pre-fix this reloaded the page ──
  await page.getByRole('button', { name: 'Move', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Move plant', exact: true })).toBeVisible();
  await page.getByLabel('Or add a new location').fill('Kitchen windowsill');
  await page.getByLabel('Pot volume (L)').fill('5.5');
  await page.getByRole('button', { name: 'Save move' }).click();
  await expect(page.getByText('Move recorded')).toBeVisible();
  await expect(
    page.getByRole('tabpanel', { name: 'Details' }).getByText('Kitchen windowsill'),
  ).toBeVisible();
  await expect(page.getByText('No location set yet.')).toHaveCount(0);
  await expect(page.getByText('Pot: 5.5 L ·')).toBeVisible();

  // ── move #2 via Enter in the new-location field (implicit submit) ──────
  await page.getByRole('button', { name: 'Move', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Move plant', exact: true })).toBeVisible();
  await page.getByLabel('Or add a new location').fill('Pantry shelf');
  await page.getByLabel('Or add a new location').press('Enter');
  await expect(
    page.getByRole('tabpanel', { name: 'Details' }).getByText('Pantry shelf'),
  ).toBeVisible();
  // hero fact shows the new current context, old one gone from current view
  await expect(page.getByRole('definition').filter({ hasText: 'Pantry shelf' })).toBeVisible();
  await expect(page.getByRole('definition').filter({ hasText: 'Kitchen windowsill' })).toHaveCount(
    0,
  );
  // pot volume carried through the form's prefill, never lost
  await expect(page.getByText('Pot: 5.5 L ·')).toBeVisible();
});
