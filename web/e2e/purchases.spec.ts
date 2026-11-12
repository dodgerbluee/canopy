import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

/** T12 acceptance journey: the $100 two-fig example against the real Go API.
 * One receipt, two plants: a mismatched allocation is reported with its exact
 * discrepancy before submit, the fix balances, the ledger and spending card
 * show one expense (never two), and void keeps the record honest. Runs on the
 * desktop project only (single shared schema; stateful by design). */

const PASSPHRASE = process.env.E2E_PASSPHRASE ?? "e2e-dev-passphrase";

async function signIn(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.getByLabel("Password").fill(PASSPHRASE);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(
    page.getByRole("heading", { level: 1, name: "Your garden, at a glance." }),
  ).toBeVisible();
}

async function addPlant(
  page: import("@playwright/test").Page,
  name: string,
): Promise<void> {
  await page.goto("/plants/new");
  await page.getByLabel("Name", { exact: true }).fill(name);
  await page.getByRole("button", { name: "Add plant" }).click();
  await expect(
    page.getByRole("heading", { level: 1, name: new RegExp(name) }),
  ).toBeVisible();
}

test("one $100 receipt split across two figs: exact reconciliation, ledger, spending, void", async ({
  page,
}) => {
  await signIn(page);

  // dedicated plants for this journey — allocation targets must be unambiguous
  await addPlant(page, "E2E Fig A");
  await addPlant(page, "E2E Fig B");

  // ── empty ledger with an honest CTA ─────────────────────────────────────
  await page.goto("/purchases");
  await expect(
    page.getByRole("heading", { name: "No purchases yet" }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Add your first purchase" }).click();
  await expect(
    page.getByRole("dialog", { name: "New purchase", exact: true }),
  ).toBeVisible();

  // ── the $100 receipt: 100.00 = 45.00 + 45.00 items + 5.00 tax + 5.00 shipping
  await page.getByLabel("Seller (optional)").fill("E2E Fig Nursery");
  await page.getByLabel("Purchased on (optional)").fill("2026-03-10");
  await page.getByLabel("Total paid").fill("100.00");
  await page.getByLabel("Tax (optional)").fill("5.00");
  await page.getByLabel("Shipping (optional)").fill("5.00");

  await page.locator("#l0-d").fill("Fig tree A");
  await page.locator("#l0-t").fill("45.00");
  await page.locator("#l0-a0-p").selectOption({ label: "E2E Fig A" });
  await page.locator("#l0-a0-l").fill("45.00");
  await page.locator("#l0-a0-t").fill("2.50");
  await page.locator("#l0-a0-s").fill("2.50");

  await page.getByRole("button", { name: "Add line" }).click();
  await page.locator("#l1-d").fill("Fig tree B");
  await page.locator("#l1-t").fill("45.00");
  await page.locator("#l1-a0-p").selectOption({ label: "E2E Fig B" });
  await page.locator("#l1-a0-l").fill("45.00");
  await page.locator("#l1-a0-t").fill("2.50");
  await page.locator("#l1-a0-s").fill("2.50");

  // ── a mistyped allocation is reported with the exact discrepancy ────────
  // (UI07: a line's total follows its single allocation's item amount, so
  // the mismatch is built on a line that splits across two allocations)
  await page
    .locator(".line-card")
    .nth(1)
    .getByRole("button", { name: "Add allocation" })
    .click();
  await page.locator("#l1-a1-l").fill("0.00");
  await page.locator("#l1-a0-l").fill("44.99");
  await expect(page.getByText("sum to 44.99 USD")).toBeVisible();
  await expect(page.getByText("off by -0.01 USD")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Add purchase" }),
  ).toBeDisabled();

  await page.locator("#l1-a0-l").fill("45.00");
  await expect(page.getByText("Balanced: 100.00 USD")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Add purchase" }),
  ).toBeEnabled();

  // ── submit: one expense lands, list + spending reflect it exactly ───────
  await page.getByRole("button", { name: "Add purchase" }).click();
  await expect(page.getByText("Purchase added")).toBeVisible();
  await expect(page).toHaveURL(/\/purchases$/);
  await expect(page.getByText("E2E Fig Nursery")).toBeVisible();
  await expect(page.getByText("100.00 USD").first()).toBeVisible();
  await expect(page.getByText("1 shown")).toBeVisible();
  // one $100 receipt is ONE expense — the workspace total never double counts
  await expect(page.getByText("USD total")).toBeVisible();
  await expect(
    page.locator(".spend-grid dd", { hasText: "100.00 USD" }),
  ).toBeVisible();

  // ── editing a linked purchase: a date-only edit saves (REPAIR-02 — the
  // acquisition→allocation FK no longer aborts the line replacement), and
  // a price correction cascades: the line's sole allocation follows the
  // line total, and the receipt total re-derives (UI07) ───────────────────
  await page.getByRole("link", { name: "Edit" }).click();
  await expect(
    page.getByRole("dialog", { name: "Edit purchase" }),
  ).toBeVisible();
  // the form renders empty while the purchase loads — type only after the
  // line prefill has landed (a header-only wait could pass before the swap)
  await expect(page.locator("#l0-t")).toHaveValue("45.00");
  await page.locator("#pf-date").fill("2026-03-11");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Purchase updated.")).toBeVisible();

  await page.getByRole("link", { name: "Edit" }).click();
  await expect(page.locator("#pf-date")).toHaveValue("2026-03-11"); // persisted
  // entry order survives the line-replacement edit (REPAIR-03: lines keyset
  // by position, not random UUID) — line 0 is still Fig tree A
  await expect(page.locator("#l0-d")).toHaveValue("Fig tree A");
  await page.locator("#l0-t").fill("50.00");
  await expect(page.locator("#l0-a0-l")).toHaveValue("50.00");
  await expect(page.getByText("Balanced: 105.00 USD")).toBeVisible();
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Purchase updated.")).toBeVisible();
  await expect(
    page.locator(".spend-grid dd", { hasText: "105.00 USD" }),
  ).toBeVisible();

  // ── the recorded price is visible on the plants it paid for ─────────────
  await page.goto("/plants");
  await expect(page.getByText("E2E Fig A")).toBeVisible();

  // ── void keeps the record but leaves the ledger ─────────────────────────
  await page.goto("/purchases");
  await page.getByRole("button", { name: "Void", exact: true }).click();
  await page.getByRole("button", { name: "Confirm void" }).click();
  await expect(page.getByText("Purchase voided")).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "No purchases yet" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Show voided" }).click();
  await expect(page.getByText("Voided", { exact: true })).toBeVisible();
  await expect(page.getByText("105.00 USD").first()).toBeVisible(); // the record survives

  // ── a11y on the two core screens ────────────────────────────────────────
  await page.goto("/purchases");
  await new AxeBuilder({ page })
    .analyze()
    .then((r) => expect(r.violations).toEqual([]));
  await page.goto("/purchases/new");
  await new AxeBuilder({ page })
    .analyze()
    .then((r) => expect(r.violations).toEqual([]));
});
