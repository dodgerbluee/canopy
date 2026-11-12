import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, test } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

/** T32.a acceptance journey: photo capture against the real Go API and the
 * real docker-sandboxed media worker (started by scripts/e2e_with_worker.sh).
 * Covers the mixed multi-upload (one bad file never sinks its sibling),
 * thumb/original delivery, reload recovery for fully received and incomplete
 * uploads, and the receipt slot on the purchase editor. One test, one sign-in
 * (the e2e API raises CANOPY_LOGIN_LIMIT above the suite's login count).
 * Runs in its own Playwright project after the stateful specs (it creates
 * plants and a purchase, which would break their empty-state assertions). */

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

async function openDetails(
  page: import("@playwright/test").Page,
): Promise<void> {
  await page.getByRole("tab", { name: "Details" }).click();
  await expect(page.getByRole("heading", { name: "Photos" })).toBeVisible();
}

const photoSection = (page: import("@playwright/test").Page) =>
  page.locator('section[aria-labelledby="photo-h"]');

const photoDialog = (page: import("@playwright/test").Page) =>
  page.getByRole("dialog", { name: "Add photos", exact: true });

async function openPhotos(
  page: import("@playwright/test").Page,
): Promise<void> {
  await page.getByRole("button", { name: "Add photos", exact: true }).click();
  await expect(photoDialog(page)).toBeVisible();
}

async function closePhotos(
  page: import("@playwright/test").Page,
): Promise<void> {
  await photoDialog(page)
    .getByRole("button", { name: "Done", exact: true })
    .click();
  await page.waitForFunction(() => {
    const titles = [...document.querySelectorAll(".dialog-title")].map(
      (el) => el.textContent,
    );
    return (
      !titles.includes("Add photos") || titles.includes("Discard changes?")
    );
  });
  const discard = page.getByRole("dialog", {
    name: "Discard changes?",
    exact: true,
  });
  if (await discard.isVisible()) {
    await discard
      .getByRole("button", { name: "Discard changes", exact: true })
      .click();
  }
  await expect(photoDialog(page)).toHaveCount(0);
}

test("capture lane: uploads, processing, delivery, reload recovery, receipts", async ({
  page,
}) => {
  test.setTimeout(300_000);
  await signIn(page);
  await addPlant(page, "Capture lane pothos");
  const detailUrl = page.url();

  // a real JPEG (page screenshot) and a file whose bytes lie about being one
  const jpeg = await page.screenshot({ type: "jpeg" });
  const notAnImage = Buffer.from(
    "these bytes are not a jpeg, whatever the name claims",
  );

  await openDetails(page);
  await openPhotos(page);
  await photoDialog(page)
    .locator("input[type=file][multiple]")
    .setInputFiles([
      { name: "good-one.jpg", mimeType: "image/jpeg", buffer: jpeg },
      { name: "not-an-image.jpg", mimeType: "image/jpeg", buffer: notAnImage },
    ]);

  // ── the bad file fails alone, with the server's honest reason ──────────
  await expect(
    photoDialog(page).getByText("content type is not allowed for this purpose"),
  ).toBeVisible();
  await expect(
    photoDialog(page).getByText("Failed", { exact: true }),
  ).toBeVisible();
  await expect(photoDialog(page).getByText("not-an-image.jpg")).toBeVisible();

  // ── the good file survives: processing → attached, thumb renders ───────
  await expect(
    photoDialog(page).getByText("Attached", { exact: true }),
  ).toBeVisible({
    timeout: 60_000,
  });
  await closePhotos(page);
  const thumb = photoSection(page).locator('img[src*="/thumb"]').first();
  await expect(thumb).toBeVisible();
  await expect
    .poll(() => thumb.evaluate((el) => (el as HTMLImageElement).naturalWidth), {
      timeout: 30_000,
    })
    .toBeGreaterThan(0); // proves the worker produced the thumb variant

  // ── the original downloads as an attachment ────────────────────────────
  const originalHref = await photoSection(page)
    .getByRole("link", { name: "Original" })
    .getAttribute("href");
  expect(originalHref).toMatch(/^\/api\/media\/[0-9a-f-]{36}\/original$/);
  const dl = await page.request.get(originalHref!);
  expect(dl.status()).toBe(200);
  expect(dl.headers()["content-disposition"]).toContain("attachment");

  // ── make cover, then a reload: the photo list is still right ───────────
  await photoSection(page).getByRole("button", { name: "Make cover" }).click();
  await expect(
    photoSection(page).locator(".badge", { hasText: "cover" }),
  ).toBeVisible();
  await page.reload();
  await openDetails(page);
  await expect(photoSection(page).locator('img[src*="/thumb"]')).toHaveCount(1);

  // ── reload recovery, fully received: reserve + PUT over the API, then
  //    reload mid-life — the queue finalizes without any reselection ──────
  const sess = (await (await page.request.get("/api/auth/session")).json()) as {
    csrfToken: string;
  };
  const csrf = sess.csrfToken;
  const gql = async (query: string, variables: object) =>
    page.request.post("/api/graphql", {
      data: { query, variables },
      headers: { "x-csrf-token": csrf },
    });

  const reserve = await gql(
    "mutation ReserveUpload($input: ReserveUploadInput!) { reserveUpload(input: $input) { asset { id } problems { code message } } }",
    {
      input: {
        purpose: "PHOTO",
        filename: "recovered.jpg",
        claimedBytes: jpeg.length,
      },
    },
  );
  const assetId = (await reserve.json()).data.reserveUpload.asset.id as string;
  const put = await page.request.put(`/api/uploads/${assetId}`, {
    data: jpeg,
    headers: { "content-type": "image/jpeg", "x-csrf-token": csrf },
  });
  expect(put.status()).toBe(200);

  await page.reload();
  await openDetails(page);
  await openPhotos(page);
  // the queue found the pending asset on its own — no file reselection asked
  await expect(
    photoDialog(page).getByText("Attached", { exact: true }),
  ).toBeVisible({
    timeout: 60_000,
  });
  await closePhotos(page);
  await expect(photoSection(page).locator('img[src*="/thumb"]')).toHaveCount(2);

  // ── reload recovery, incomplete: a bare reservation asks for the file ──
  const bare = await gql(
    "mutation ReserveUpload($input: ReserveUploadInput!) { reserveUpload(input: $input) { asset { id } problems { code message } } }",
    { input: { purpose: "PHOTO", filename: "halfway.jpg", claimedBytes: 10 } },
  );
  expect((await bare.json()).data.reserveUpload.asset.id).toBeTruthy();
  await page.reload();
  await openDetails(page);
  await openPhotos(page);
  await expect(
    photoDialog(page).getByText("Needs file", { exact: true }),
  ).toBeVisible();
  await expect(photoDialog(page).getByText("Reselect file")).toBeVisible();
  await expect(photoDialog(page).getByText("never completed")).toBeVisible();
  await closePhotos(page);

  // ── compare: picker → deep-linked pair, keyboard overlay, mismatch label,
  //    and a deleted selection recovering to the picker ────────────────────
  await page.getByRole("link", { name: "Compare photos" }).click();
  await expect(
    page.getByRole("heading", { name: "Compare photos" }),
  ).toBeVisible();
  await expect(
    page.getByText("Choose a left and a right photo to compare."),
  ).toBeVisible();
  await page.getByRole("button", { name: "Use as left" }).first().click();
  await page.getByRole("button", { name: "Use as right" }).nth(1).click();
  await expect(page.getByRole("figure")).toHaveCount(2);
  await expect(page.getByText("Same viewpoint")).toBeVisible();
  await expect(page.getByText(/\d+(\.\d+)?\s*%/)).toHaveCount(0); // no invented growth metric
  await new AxeBuilder({ page })
    .analyze()
    .then((r) => expect(r.violations).toEqual([]));

  await page.getByRole("button", { name: "Overlay slider" }).click();
  await page.getByLabel("Reveal right photo percentage").focus();
  await page.keyboard.press("ArrowRight"); // native range: arrows move the wipe
  await expect(page.locator("img").nth(1)).toHaveAttribute(
    "style",
    /inset\((0 0 0 51%|0px 0px 0px 51%)\)/,
  );

  const plantId = /\/plants\/([^/]+)/.exec(detailUrl)![1];
  const series = (
    await (
      await gql(
        "query($plantId: ID!, $first: Int, $filter: JournalFilter) { journal(plantId: $plantId, first: $first, filter: $filter) { items { __typename ... on PhotoEntry { photo { id viewpoint revision } } } } }",
        { plantId, first: 10, filter: { entryTypes: ["PHOTO"] } },
      )
    ).json()
  ).data.journal.items as {
    photo?: { id: string; viewpoint: string; revision: number };
  }[];
  const whole = series.find((i) => i.photo?.viewpoint === "WHOLE")!.photo!;
  const bump = await gql(
    "mutation($input: UpdatePhotoInput!) { updatePhoto(input: $input) { photo { id viewpoint } problems { code message } } }",
    {
      input: {
        id: whole.id,
        expectedRevision: whole.revision,
        viewpoint: "LEAF",
      },
    },
  );
  expect((await bump.json()).data.updatePhoto.problems ?? []).toEqual([]);
  const compareUrl = page.url();
  await page.reload();
  await expect(page.getByText("Viewpoints differ")).toBeVisible();

  // a selection that cannot resolve recovers to the picker while candidates remain
  await page.goto(
    compareUrl.replace(/b=[^&]+/, "b=00000000-0000-0000-0000-000000000000"),
  );
  await expect(page.getByText("no longer available")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Use as left" }).first(),
  ).toBeVisible();

  await page.goto(detailUrl);
  await openDetails(page);
  await page.getByRole("button", { name: "Remove" }).first().click();
  await page.getByRole("button", { name: "Confirm remove" }).click();
  await expect(page.getByText("Photo removed")).toBeVisible();
  await page.goto(compareUrl); // the deep link now points at a deleted photo
  await expect(page.getByText("no longer available")).toBeVisible();
  await expect(page.getByText("A comparison needs two")).toBeVisible();

  // ── a11y: the detail screen with a live queue passes axe ───────────────
  // Reduced motion pins transition-duration to ~0 so axe never samples a
  // mid-transition blend (e.g. the selected tab chip fading to --accent).
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(detailUrl);
  await expect(
    page.getByRole("heading", { level: 1, name: "Capture lane pothos" }),
  ).toBeVisible();
  await new AxeBuilder({ page })
    .analyze()
    .then((r) => expect(r.violations).toEqual([]));
  await openDetails(page);
  await new AxeBuilder({ page })
    .analyze()
    .then((r) => expect(r.violations).toEqual([]));

  // ── receipt slot: attach a receipt photo to a purchase ─────────────────
  await addPlant(page, "Capture receipt lemon");
  await page.goto("/purchases/new");
  await page.getByLabel("Seller (optional)").fill("Capture lane nursery");
  await page.getByLabel("Total paid").fill("25.00");
  await page.locator("#l0-d").fill("Lemon tree");
  await page.locator("#l0-t").fill("25.00");
  await page
    .locator("#l0-a0-p")
    .selectOption({ label: "Capture receipt lemon" });
  await page.locator("#l0-a0-l").fill("25.00");
  await expect(page.getByText("Balanced: 25.00 USD")).toBeVisible();
  await page.getByRole("button", { name: "Add purchase" }).click();
  await expect(page.getByText("Purchase added")).toBeVisible();

  // edit it and attach the receipt photo through the media pipeline
  await page
    .getByRole("row", { name: /Capture lane nursery/ })
    .getByRole("link", { name: "Edit" })
    .click();
  await expect(
    page.getByRole("dialog", { name: "Edit purchase", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("No receipt images yet.")).toBeVisible();
  await page.locator("receipt-upload input[type=file]").setInputFiles({
    name: "receipt.png",
    mimeType: "image/png",
    buffer: readFileSync(resolve(__dirname, "fixtures", "receipt.png")),
  });
  await expect(page.getByText("Receipt image attached.")).toBeVisible({
    timeout: 60_000,
  });
  const href = await page
    .getByRole("link", { name: "View original" })
    .getAttribute("href");
  const receiptDl = await page.request.get(href!);
  expect(receiptDl.status()).toBe(200); // receipts serve their original only

  // ── T32.b OCR review: extraction is offered, advisory, and only writes
  // through the explicit Apply the owner clicks. The worker OCRs the fixture
  // with the local sandbox path; with a text engine configured (the T34 e2e
  // stub) the OCR text is structured through it, so the draft carries
  // total/currency/date, no lines, and needsReconciliation=true either way.
  await expect(
    page.getByRole("button", { name: "Extract details from receipt" }),
  ).toBeVisible({ timeout: 15_000 });
  await page
    .getByRole("button", { name: "Extract details from receipt" })
    .click();
  await page
    .getByRole("dialog", { name: "Extract details from receipt", exact: true })
    .getByRole("button", { name: "Extract details", exact: true })
    .click();
  const card = page.getByRole("dialog", {
    name: "Extracted from receipt",
    exact: true,
  });
  await expect(
    page.getByRole("dialog", { name: "Extracted from receipt", exact: true }),
  ).toBeVisible({ timeout: 90_000 });
  await expect(card.getByText("28.00 USD")).toBeVisible();
  await expect(card.getByText("2026-05-14")).toBeVisible();
  await expect(card.getByText("Needs reconciliation")).toBeVisible();
  await expect(card.getByText("No line items were read")).toBeVisible();
  await expect(card.getByText("e2e-stub")).toBeVisible();
  // the form still holds the owner's values — the draft has written nothing
  await expect(page.locator("#pf-total")).toHaveValue("25.00");

  await page
    .getByRole("dialog", { name: "Extracted from receipt", exact: true })
    .getByRole("button", { name: "Apply extracted details", exact: true })
    .click();
  await expect(page.locator("#pf-total")).toHaveValue("28.00");
  await expect(page.locator("#pf-cur")).toHaveValue("USD");
  await expect(page.locator("#pf-date")).toHaveValue("2026-05-14");
  await expect(
    page.getByRole("dialog", { name: "Extracted from receipt", exact: true }),
  ).not.toBeVisible();

  // the owner reconciles the line to the extracted total and saves normally
  await page.locator("#l0-t").fill("28.00");
  await page.locator("#l0-a0-l").fill("28.00");
  await expect(page.getByText("Balanced: 28.00 USD")).toBeVisible();
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Purchase updated")).toBeVisible();
  await expect(
    page.getByRole("row", { name: /Capture lane nursery/ }),
  ).toContainText("28.00");
});
