import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import pg from "pg";

/** T07 acceptance: real login/logout/expired-session flows against the real
 * Go API (global-setup bootstraps a disposable schema + owner), plus both
 * themes and axe. */

const PASSPHRASE = process.env.E2E_PASSPHRASE ?? "e2e-dev-passphrase";
const DSN = process.env.E2E_PG_DSN ?? "";

async function signIn(page: import("@playwright/test").Page): Promise<void> {
  await page.getByLabel("Password").fill(PASSPHRASE);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(
    page.getByRole("heading", { level: 1, name: "Your garden, at a glance." }),
  ).toBeVisible();
}

test("login → shell; refresh, deep link and back all hold; logout ends it", async ({
  page,
}) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/login\?returnUrl=%2F/);
  // cold anonymous visit must not claim an expired session
  await expect(page.getByRole("status")).toHaveCount(0);

  await signIn(page);
  await expect(page.getByRole("navigation", { name: "Primary" })).toBeVisible();
  const axe = await new AxeBuilder({ page }).analyze();
  expect(axe.violations).toEqual([]);

  // refresh keeps the session (server cookie, not browser storage)
  await page.reload();
  await expect(page.getByRole("navigation", { name: "Primary" })).toBeVisible();

  // deep link straight into a guarded route
  await page.goto("/settings");
  await expect(
    page.getByRole("heading", { level: 1, name: "Settings" }),
  ).toBeVisible();
  // T08 round-trip: generated TS document → real gqlgen server over HTTP
  await expect(page.getByText("My garden")).toBeVisible();

  // back returns to overview without re-login
  await page.goBack();
  await expect(
    page.getByRole("heading", { level: 1, name: "Your garden, at a glance." }),
  ).toBeVisible();

  // logout from settings. The rail (and its Settings link) only exists at
  // ≥768px; at mobile size the same destination lives in the Account menu.
  const width = page.viewportSize()?.width ?? 1280;
  if (width < 768) {
    await page.getByRole("button", { name: "Account" }).click();
    await page.getByRole("menuitem", { name: "Settings" }).click();
  } else {
    await page.getByRole("link", { name: "Settings" }).first().click();
  }
  await expect(
    page.getByRole("heading", { level: 1, name: "Settings" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login/);

  // back must not resurrect a revoked session
  await page.goBack();
  await expect(page).toHaveURL(/\/login/);
});

test("idle-expired session redirects to login with an honest notice", async ({
  page,
}) => {
  await page.goto("/login");
  await signIn(page);
  await page.goto("/plants/new");
  const entry = page.getByRole("dialog", { name: "Add a plant", exact: true });
  await expect(entry).toBeVisible();
  await entry
    .getByLabel("Name", { exact: true })
    .fill("Unsaved expired-session draft");

  // age the session past the 12h idle window in the disposable schema
  // (node-pg does not apply search_path from the URL; set it explicitly)
  const client = new pg.Client(DSN);
  await client.connect();
  await client.query(`SET search_path TO ${process.env.E2E_SCHEMA}`);
  const aged = await client.query(
    `UPDATE sessions SET last_seen_at = now() - interval '13 hours'`,
  );
  expect(aged.rowCount).toBeGreaterThan(0);
  await client.end();

  // The rejected save is busy and dirty. Expiry must bypass the draft guard,
  // dispose the modal, and show the existing honest sign-in notice.
  await entry.getByRole("button", { name: "Add plant", exact: true }).click();
  await expect(page).toHaveURL(/\/login/);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("status")).toContainText("session ended");
});

test("login page renders and passes axe in both themes", async ({ page }) => {
  for (const theme of ["dark", "light"] as const) {
    await page.addInitScript(
      (t) => localStorage.setItem("canopy-theme", t),
      theme,
    );
    await page.goto("/login");
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    const axe = await new AxeBuilder({ page }).analyze();
    expect(axe.violations).toEqual([]);
  }
});

test("no session or csrf material in browser persistent storage", async ({
  page,
}) => {
  await page.goto("/login");
  await signIn(page);
  const stored = await page.evaluate(() => ({
    local: { ...localStorage },
    session: { ...sessionStorage },
  }));
  const dump = JSON.stringify(stored);
  expect(dump).not.toContain("csrf");
  expect(dump).not.toContain("canopy_session");
  // only the nonsecret theme preference may persist
  for (const key of Object.keys(stored.local)) expect(key).toBe("canopy-theme");
});
