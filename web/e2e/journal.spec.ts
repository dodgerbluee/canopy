import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

/** T19 acceptance journey: log from a phone viewport, backdate, correct and
 * void records, and the timeline is still right after a reload — against the
 * real Go API. Plant-scoped fixtures keep this independent of the other
 * stateful specs (single shared disposable schema, workers=1). */

const PASSPHRASE = process.env.E2E_PASSPHRASE ?? "e2e-dev-passphrase";

async function signIn(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.getByLabel("Password").fill(PASSPHRASE);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(
    page.getByRole("heading", { level: 1, name: "Your garden, at a glance." }),
  ).toBeVisible();
}

test("phone quick-log, backdating, correction, void — timeline correct after reload", async ({
  page,
}) => {
  await signIn(page);

  // ── own plant so the timeline count is deterministic ───────────────────
  await page.goto("/plants/new");
  await page
    .getByLabel("Name", { exact: true })
    .fill("Journal journey monstera");
  await page.getByRole("button", { name: "Add plant" }).click();
  await expect(page).toHaveURL(/\/plants\/[0-9a-f-]{36}$/);
  const detailUrl = page.url();

  // ── Journey tab is the default view of a plant, empty state is honest ──
  await expect(page.getByRole("tab", { name: "Journey" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByText("No journal yet")).toBeVisible();

  // ── backdated water entry, logged from a phone ─────────────────────────
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "Quick log", exact: true }).click();
  await page.getByLabel("What happened").selectOption({ value: "WATER" });
  await page.getByLabel("Date").fill("2026-08-05"); // backdated, no time given
  await page.getByLabel("Quantity").fill("500");
  await page.getByLabel("Unit").selectOption({ value: "ML" });
  await page.getByRole("button", { name: "Log it" }).click();
  await expect(page.getByText("Logged")).toBeVisible();
  await expect(page.getByText("August 2026")).toBeVisible();
  await expect(page.getByText("500 ml", { exact: true })).toBeVisible(); // badge, not the server summary

  // ── a plain note, today ────────────────────────────────────────────────
  await page.getByRole("button", { name: "Quick log", exact: true }).click();
  await page.getByLabel("What happened").selectOption({ value: "NOTE" });
  await page.getByLabel("Note").fill("Rotated toward the light");
  await page.getByRole("button", { name: "Log it" }).click();
  await expect(
    page.getByText("Rotated toward the light").first(),
  ).toBeVisible();

  // ── a measurement ──────────────────────────────────────────────────────
  await page.getByRole("button", { name: "Quick log", exact: true }).click();
  await page.getByRole("button", { name: "Measurement", exact: true }).click();
  await page.getByLabel("Value").fill("12.5");
  await page.getByLabel("Observed on").fill("2026-10-21");
  await page.getByRole("button", { name: "Log it" }).click();
  await expect(page.getByText("12.5 cm", { exact: true })).toBeVisible();
  await expect(page.getByText("3 shown")).toBeVisible();

  // ── reload: the timeline is still exactly right ────────────────────────
  await page.reload();
  await expect(page.getByRole("tab", { name: "Journey" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByText("August 2026")).toBeVisible();
  await expect(page.getByText("October 2026")).toBeVisible();
  await expect(
    page.getByText("Rotated toward the light").first(),
  ).toBeVisible();
  await expect(page.getByText("3 shown")).toBeVisible();

  // ── correct the water record (revision-guarded edit) ───────────────────
  const waterCard = page.locator("li.card", { hasText: "Watered" });
  await waterCard.getByRole("button", { name: "Edit" }).click();
  await expect(page.getByLabel("Date")).toHaveValue("2026-08-05"); // prefilled
  await page.getByLabel("Note").fill("Soak and drain");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Activity corrected")).toBeVisible();
  await expect(page.getByText("Soak and drain")).toBeVisible();

  // ── void: the card leaves the default timeline, history stays server-side
  await waterCard.getByRole("button", { name: "Void" }).click();
  await page
    .getByRole("dialog", { name: "Void this activity?", exact: true })
    .getByRole("button", { name: "Void activity", exact: true })
    .click();
  await expect(page.getByText("Activity voided")).toBeVisible();
  await expect(page.getByText("Watered")).toHaveCount(0);
  await expect(page.getByText("2 shown")).toBeVisible();

  // ── Details tab still works (tabs did not eat the existing screen) ─────
  await page.getByRole("tab", { name: "Details" }).click();
  await expect(
    page.getByRole("heading", { name: "Current context" }),
  ).toBeVisible();

  // ── workspace journal via mobile nav, with plant picker ────────────────
  await page
    .getByRole("navigation", { name: "Primary mobile" })
    .getByRole("link", { name: "Journal" })
    .click();
  await expect(page).toHaveURL(/\/journal$/);
  await expect(
    page.getByText("Rotated toward the light").first(),
  ).toBeVisible();
  await page.getByRole("button", { name: "Quick log", exact: true }).click();
  await expect(page.getByLabel("Plant")).toBeVisible(); // workspace scope asks which plant

  // ── a11y on the journal screen ─────────────────────────────────────────
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(detailUrl);
  await expect(
    page.getByRole("heading", { level: 1, name: "Journal journey monstera" }),
  ).toBeVisible();
  await new AxeBuilder({ page })
    .analyze()
    .then((r) => expect(r.violations).toEqual([]));
  await page.goto("/journal");
  await expect(
    page.getByRole("heading", { level: 1, name: "Journal" }),
  ).toBeVisible();
  await new AxeBuilder({ page })
    .analyze()
    .then((r) => expect(r.violations).toEqual([]));
});
