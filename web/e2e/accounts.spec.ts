import { expect, test } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const PASSWORD = 'auth-e2e-passphrase';
const MEMBER_PASSWORD = 'member-e2e-passphrase';

test('empty installation → administrator → member access → account controls', async ({ page }) => {
  const options = await page.request.get('/api/auth/options');
  expect(
    (await options.json()).setupRequired,
    'This suite requires a disposable EMPTY installation.',
  ).toBe(true);
  await page.goto('/');
  await expect(page).toHaveURL(/\/setup$/);
  await expect(page.getByRole('heading', { name: 'Create your account' })).toBeVisible();
  expect((await new AxeBuilder({ page }).analyze()).violations).toEqual([]);
  await page.screenshot({ path: '../.artifacts/agents/AUTH01/setup-desktop.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: '../.artifacts/agents/AUTH01/setup-mobile.png', fullPage: true });
  await page.getByLabel('Username', { exact: true }).fill('gardenadmin');
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await page.getByLabel('Confirm password', { exact: true }).fill(PASSWORD);
  await page.locator('.garden-preferences summary').click();
  await page.getByLabel('Timezone', { exact: true }).fill('America/Chicago');
  await page.getByRole('button', { name: 'Create account' }).click();
  await expect(page.getByRole('heading', { name: 'Your garden, at a glance.' })).toBeVisible();
  await page.goto('/settings');
  const account = page.locator('[aria-labelledby="account-heading"]');
  await expect(account.getByText('People in your garden', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Add user', exact: true }).click();
  const form = page.getByRole('dialog', { name: 'Add a garden member' });
  await expect(form).toBeVisible();
  await form.getByLabel('Name', { exact: true }).fill('Garden helper');
  await form.getByLabel('Username', { exact: true }).fill('helper');
  await form.getByLabel('Initial password', { exact: true }).fill(MEMBER_PASSWORD);
  await form.getByRole('button', { name: 'Save account' }).click();
  await expect(form).not.toBeVisible();
  await expect(account.getByText('Garden helper', { exact: true })).toBeVisible();
  await page.screenshot({
    path: '../.artifacts/agents/AUTH01/accounts-mobile.png',
    fullPage: true,
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await account.scrollIntoViewIfNeeded();
  expect((await new AxeBuilder({ page }).include('account-settings').analyze()).violations).toEqual(
    [],
  );
  await page.screenshot({
    path: '../.artifacts/agents/AUTH01/accounts-desktop.png',
    fullPage: true,
  });
  await page.getByRole('button', { name: 'Connect OIDC', exact: true }).click();
  const oidc = page.getByRole('dialog', { name: 'OIDC connection', exact: true });
  await expect(oidc).toBeVisible();
  await expect(oidc.getByText(/http:\/\/localhost:7081\/api\/auth\/oidc\/callback/)).toBeVisible();
  await expect(oidc.getByLabel('Client secret', { exact: true })).toHaveAttribute(
    'type',
    'password',
  );
  await page.screenshot({ path: '../.artifacts/agents/AUTH01/oidc-modal.png' });
  await oidc.getByRole('button', { name: 'Cancel', exact: true }).click();
  await page.getByRole('button', { name: 'Edit gardenadmin', exact: true }).click();
  const edit = page.getByRole('dialog', { name: 'Edit account', exact: true });
  await edit.getByLabel('Disable this account').check();
  await edit.getByRole('button', { name: 'Save account' }).click();
  await expect(edit.getByRole('alert')).toContainText('keep at least one active administrator');
  await edit.getByRole('button', { name: 'Cancel', exact: true }).click();
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click();
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect(page).toHaveURL(/\/login/);
  await page.getByLabel('Username', { exact: true }).fill('helper');
  await page.getByLabel('Password', { exact: true }).fill(MEMBER_PASSWORD);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Your garden, at a glance.' })).toBeVisible();
  await page.goto('/settings');
  await expect(account.getByText('Garden helper', { exact: true })).toBeVisible();
  await expect(account.getByText('People in your garden', { exact: true })).toHaveCount(0);
  expect((await page.request.get('/api/auth/users')).status()).toBe(403);
  await page.getByRole('button', { name: 'Change password', exact: true }).click();
  const password = page.getByRole('dialog', { name: 'Change your password' });
  await password.getByLabel('Current password', { exact: true }).fill(MEMBER_PASSWORD);
  await password.getByLabel('New password', { exact: true }).fill('updated-member-passphrase');
  await password.getByLabel('Confirm password', { exact: true }).fill('updated-member-passphrase');
  await password.getByLabel('Current password', { exact: true }).fill('incorrect-password');
  await password.getByRole('button', { name: 'Change password', exact: true }).click();
  await expect(password.getByRole('alert')).toContainText('Invalid credentials');
  await password.getByLabel('Current password', { exact: true }).fill(MEMBER_PASSWORD);
  await password.getByRole('button', { name: 'Change password', exact: true }).click();
  await expect(password).not.toBeVisible();
  await expect(account.getByRole('status')).toContainText('Password changed');
});
