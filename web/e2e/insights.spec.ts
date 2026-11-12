import { expect, test, type Page } from "@playwright/test";
import { resolve } from "node:path";

/** T34 acceptance journey: the insights tab against a real worker and a
 * stub AI provider (web/e2e/stub-provider.mjs) that speaks the real wire
 * contract. Covers: capability honesty, cancel mid-run, idempotency →
 * explicit rerun, structured findings with evidence trails, accept →
 * one care task (duplicate accept refused), dismiss, and the daily budget
 * refusing a third run. Stale runs and vision pairing are server/unit
 * tested; the e2e engine has no vision provider, which is itself asserted.
 *
 * Runs last among seeding specs (today.spec asserts an empty suggestions
 * slot) — see playwright.e2e.config.ts. */

const PASSPHRASE = process.env.E2E_PASSPHRASE ?? "e2e-dev-passphrase";

const iso = (d: Date) => d.toISOString().slice(0, 10);
const TODAY = new Date();
const TODAY_WD = [
  "SUNDAY",
  "MONDAY",
  "TUESDAY",
  "WEDNESDAY",
  "THURSDAY",
  "FRIDAY",
  "SATURDAY",
][TODAY.getUTCDay()];

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByLabel("Password").fill(PASSPHRASE);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(
    page.getByRole("heading", { level: 1, name: "Your garden, at a glance." }),
  ).toBeVisible();
}

async function startReview(page: Page): Promise<void> {
  await page
    .getByRole("button", { name: "Review this plant’s history", exact: true })
    .click();
  await page
    .getByRole("dialog", { name: "Review this plant’s history", exact: true })
    .getByRole("button", { name: "Start review", exact: true })
    .click();
}

async function rerunReview(page: Page): Promise<void> {
  await page
    .getByRole("button", { name: "Run again (billed again)", exact: true })
    .click();
  await page
    .getByRole("dialog", { name: "Review history again", exact: true })
    .getByRole("button", { name: "Run again (billed again)", exact: true })
    .click();
}

/** Same seeding path care.spec uses, under a distinct species so the plan
 * editor's profile dropdown stays unambiguous next to care.spec's profile. */
async function seedProfile(page: Page): Promise<void> {
  await page.evaluate(
    async ([weekday, reviewedOn]) => {
      const csrf = (await (await fetch("/api/auth/session")).json())
        .csrfToken as string;
      const gql = async (query: string, variables: unknown) => {
        const res = await fetch("/api/graphql", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
          body: JSON.stringify({ query, variables }),
        });
        const json = (await res.json()) as {
          data?: Record<string, any>;
          errors?: unknown[];
        };
        if (json.errors?.length) throw new Error(JSON.stringify(json.errors));
        return json.data!;
      };
      const src = await gql(
        `mutation ($input: CreateSourceInput!) {
          createSource(input: $input) {
            source { currentVersion { id } }
            problems { code message }
          }
        }`,
        {
          input: {
            kind: "OWNER_NOTE",
            title: "E2E insights notes",
            citation: "E2E Insights Note, 2026",
            summary: "Journey-local guidance for the insights run.",
            retrievedOn: reviewedOn,
          },
        },
      );
      if (src.createSource.problems.length)
        throw new Error(JSON.stringify(src.createSource.problems));
      const sourceVersionId = src.createSource.source.currentVersion
        .id as string;

      const draft = await gql(
        `mutation ($input: CreateCareProfileDraftInput!) {
          createCareProfileDraft(input: $input) {
            profile { id revision }
            problems { code field message }
          }
        }`,
        {
          input: {
            category: "OTHER",
            species: "E2E Insights Monstera",
            environment: { situations: ["INDOOR"], hemispheres: ["NORTHERN"] },
            rules: [
              {
                ruleKey: "water",
                kind: "ACTION",
                activityKind: "WATER",
                recurrence: { weeklyDays: { weekdays: [weekday] } },
                guidanceText: "Soak and drain when the top 3 cm are dry.",
                sourceVersionIds: [sourceVersionId],
              },
            ],
          },
        },
      );
      if (draft.createCareProfileDraft.problems.length)
        throw new Error(JSON.stringify(draft.createCareProfileDraft.problems));
      const profile = draft.createCareProfileDraft.profile as {
        id: string;
        revision: number;
      };

      const pub = await gql(
        `mutation ($input: PublishCareProfileInput!) {
          publishCareProfile(input: $input) {
            profile { currentVersion { id } }
            problems { code field message }
          }
        }`,
        {
          input: {
            profileId: profile.id,
            expectedRevision: profile.revision,
            reviewerLabel: "E2E Reviewer",
            reviewedOn,
          },
        },
      );
      if (pub.publishCareProfile.problems.length)
        throw new Error(JSON.stringify(pub.publishCareProfile.problems));
    },
    [TODAY_WD, iso(TODAY)],
  );
}

async function openInsights(page: Page): Promise<void> {
  await page.getByRole("tab", { name: "Insights" }).click();
}

test("insights journey: honest engines, cancel, rerun, findings, accept/dismiss, budget", async ({
  page,
  context,
}) => {
  test.setTimeout(120_000); // two real stub runs with polling live inside
  await signIn(page);
  await seedProfile(page);

  // ── plant + one quick-logged WATER activity (the run's evidence) + a plan
  //    (accept needs a plan to host the task) ─────────────────────────────
  await page.goto("/plants/new");
  await page
    .getByLabel("Name", { exact: true })
    .fill("Insights journey monstera");
  await page.getByRole("button", { name: "Add plant" }).click();
  await expect(page).toHaveURL(/\/plants\/[0-9a-f-]{36}$/);
  const detailUrl = page.url();

  await page.getByRole("button", { name: "Quick log", exact: true }).click();
  await page.getByLabel("What happened").selectOption({ value: "WATER" });
  await page.getByLabel("Quantity").fill("500");
  await page.getByLabel("Unit").selectOption({ value: "ML" });
  await page.getByRole("button", { name: "Log it" }).click();
  await expect(page.getByText("Logged")).toBeVisible();

  await page.getByRole("tab", { name: "Care" }).click();
  await page.getByRole("link", { name: "Create care plan" }).click();
  await expect(page).toHaveURL(/\/care\/plan\/[0-9a-f-]{36}$/);
  const profileValue = await page
    .locator("#pe-profile option", { hasText: "E2E Insights Monstera · v1" })
    .getAttribute("value");
  await page.selectOption("#pe-profile", profileValue!);
  await page.getByRole("button", { name: "Create plan" }).click();
  await expect(
    page.getByText("Plan created — the agenda fills from today."),
  ).toBeVisible();

  // ── insights tab: engine state stated honestly, including what is off ──
  await page.goto(detailUrl);
  await openInsights(page);
  await expect(page.getByText("available · e2e-stub")).toBeVisible();
  await expect(
    page.getByText("No vision provider is configured"),
  ).toBeVisible();
  await expect(
    page.getByText("No identification provider is configured"),
  ).toBeVisible();

  // ── run 1: cancel mid-run (the stub sleeps 4s; the first poll catches
  //    it Running) ─────────────────────────────────────────────────────────
  await startReview(page);
  await expect(page.getByText("Queued — waiting for a worker")).toBeVisible();
  await expect(page.getByText("Running", { exact: true })).toBeVisible({
    timeout: 10_000,
  });
  await page.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByText("Cancelled", { exact: true })).toBeVisible();

  // ── same inputs again: the server hands back the existing run instead of
  //    double-billing; the explicit rerun is the owner's call ─────────────
  await startReview(page);
  await expect(
    page.getByText("already exists; force a rerun to re-bill"),
  ).toBeVisible();
  await rerunReview(page);
  await expect(page.getByText("Queued — waiting for a worker")).toBeVisible();
  await expect(page.getByText("Done", { exact: true })).toBeVisible({
    timeout: 20_000,
  });

  // ── findings: observations with evidence trails, an unsupported claim
  //    called out, a qualified hypothesis, citations, missing facts ───────
  await expect(
    page.getByText("Recent care activity is on schedule.", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("evidence: activity").first()).toBeVisible();
  await expect(
    page.getByText("no evidence referenced — treat as unsupported", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByText("medium confidence", { exact: true }),
  ).toBeVisible();
  await expect(
    page
      .getByText("Irregular light may correlate with leaf condition.")
      .first(),
  ).toBeVisible();
  await expect(page.getByText("E2E care notes").first()).toBeVisible();
  await expect(
    page
      .getByRole("region", { name: "History review" })
      .getByText("No humidity readings are recorded.")
      .first(),
  ).toBeVisible();

  // ── suggestions projected from the run's follow-up checks ──────────────
  await expect(
    page.getByRole("heading", { name: "Check the leaf tips again in 7 days." }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", {
      name: "Photograph the whole plant in 14 days.",
    }),
  ).toBeVisible();

  // ── keyboard review trail (T34 verify): Tab-only reach of every control;
  //    screenshots land in .artifacts/agents/T34/keyboard for owner review ─
  const kbDir = resolve(__dirname, "../../.artifacts/agents/UI02/keyboard");
  const stops: string[] = [];
  await page.getByRole("tab", { name: "Insights" }).focus();
  for (let i = 0; i < 16; i++) {
    await page.keyboard.press("Tab");
    stops.push(
      await page.evaluate(() => {
        const el = document.activeElement as HTMLElement | null;
        if (!el) return "(none)";
        const label =
          el.getAttribute("aria-label") ?? el.textContent?.trim() ?? "";
        return `${el.tagName.toLowerCase()} ${label.slice(0, 60)} :: ${el.outerHTML.slice(0, 90)}`;
      }),
    );
    await page.screenshot({
      path: resolve(kbDir, `focus-${String(i + 1).padStart(2, "0")}.png`),
    });
  }
  const trail = stops.join("\n");
  for (const control of [
    "Review this plant’s history",
    "Run again (billed again)",
    "Accept proposal",
    "Dismiss",
  ])
    expect(
      trail,
      `no Tab stop landed on ${control} — trail:\n${trail}`,
    ).toContain(control);

  // Due-date entry lives in its own modal; keyboard activation and Tab
  // must reach it without submitting the proposal.
  const acceptTrigger = page
    .locator("article.card", {
      hasText: "Check the leaf tips again in 7 days.",
    })
    .getByRole("button", { name: "Accept proposal", exact: true });
  await acceptTrigger.focus();
  await page.keyboard.press("Enter");
  const acceptDialog = page.getByRole("dialog", {
    name: "Accept proposal",
    exact: true,
  });
  await expect(acceptDialog).toBeVisible();
  const modalStops: string[] = [];
  for (let i = 0; i < 6; i++) {
    await page.keyboard.press("Tab");
    modalStops.push(
      await page.evaluate(() => document.activeElement?.outerHTML ?? ""),
    );
  }
  expect(modalStops.join("\n")).toContain('type="date"');
  await acceptDialog
    .getByRole("button", { name: "Cancel", exact: true })
    .click();
  await expect(acceptDialog).toHaveCount(0);

  // a second page holds the same suggestion list: the duplicate-accept
  // refusal is tested against a genuinely stale view, as in real use
  const page2 = await context.newPage();
  await page2.goto(detailUrl);
  await openInsights(page2);
  await expect(
    page2.getByRole("heading", {
      name: "Check the leaf tips again in 7 days.",
    }),
  ).toBeVisible();

  // ── accept: exactly one check task, due today, on the plant's plan ─────
  await page
    .locator("article.card", {
      hasText: "Check the leaf tips again in 7 days.",
    })
    .getByRole("button", { name: "Accept proposal" })
    .click();
  await page
    .getByRole("dialog", { name: "Accept proposal", exact: true })
    .getByRole("button", { name: "Accept proposal", exact: true })
    .click();
  await expect(page.getByText("Task added to care — due")).toBeVisible();

  // ── duplicate accept from the stale page: refused, task named ──────────
  await page2
    .locator("article.card", {
      hasText: "Check the leaf tips again in 7 days.",
    })
    .getByRole("button", { name: "Accept proposal" })
    .click();
  await page2
    .getByRole("dialog", { name: "Accept proposal", exact: true })
    .getByRole("button", { name: "Accept proposal", exact: true })
    .click();
  await expect(
    page2.getByText("suggestion already accepted; its task already exists."),
  ).toBeVisible();
  await page2.close();

  // ── dismiss is final and the list re-reads ─────────────────────────────
  await page
    .locator("article.card", {
      hasText: "Photograph the whole plant in 14 days.",
    })
    .getByRole("button", { name: "Dismiss" })
    .click();
  await page
    .getByRole("dialog", { name: "Dismiss suggestion", exact: true })
    .getByRole("button", { name: "Dismiss", exact: true })
    .click();
  await expect(
    page.getByText("Suggestion dismissed — dismissal is final."),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", {
      name: "Photograph the whole plant in 14 days.",
    }),
  ).toHaveCount(0);

  // ── budget: runs are refused before any billing once the day's cap is
  //    hit. The suite shares one workspace and one local day, and the
  //    capture journey's receipt extraction consumed a run too, so the
  //    remaining count is discovered by clicking, not hardcoded.
  let refusedByBudget = false;
  const done = page.getByText("Done", { exact: true });
  for (let i = 0; i < 6 && !refusedByBudget; i++) {
    await rerunReview(page);
    let billed = true;
    try {
      // an allowed run leaves the terminal 'Done' state as it queues; the
      // worker is quick, so catch the departure, not the transient labels
      await expect(done).not.toBeVisible({ timeout: 3_000 });
    } catch {
      billed = false;
    }
    if (billed) {
      await expect(done).toBeVisible({ timeout: 30_000 });
    } else {
      await page
        .getByRole("dialog", { name: "Review history again", exact: true })
        .getByText(
          /Daily analysis limit reached \(\d+ of \d+ runs used today\)/,
        )
        .waitFor({ state: "visible", timeout: 3_000 });
      refusedByBudget = true;
    }
  }
  expect(refusedByBudget).toBe(true);
});
