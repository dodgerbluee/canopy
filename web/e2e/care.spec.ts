import { expect, test, type Locator } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";

/** T25 acceptance journey: seed a reviewed care profile through the API,
 * create a plan through the editor (with the server-computed preview),
 * then complete / check / snooze / skip real occurrences in the agenda —
 * against the real Go API. One shared disposable schema: this spec seeds a
 * profile, plant and occurrences, so it runs in a dependent project after
 * the empty-state specs (see playwright.e2e.config.ts) and archives its
 * plant at the end. */

const PASSPHRASE = process.env.E2E_PASSPHRASE ?? "e2e-dev-passphrase";

// Playwright runs UTC (config timezoneId); the app groups by browser-local
// day, so these match the agenda's own grouping.
const iso = (d: Date) => d.toISOString().slice(0, 10);
const TODAY = new Date();
const day = (offset: number) =>
  iso(new Date(TODAY.getTime() + offset * 86_400_000));
const WEEKDAYS = [
  "SUNDAY",
  "MONDAY",
  "TUESDAY",
  "WEDNESDAY",
  "THURSDAY",
  "FRIDAY",
  "SATURDAY",
];
const TODAY_WD = WEEKDAYS[TODAY.getUTCDay()];
const WD_SHORT = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"][
  TODAY.getUTCDay()
];

async function signIn(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.getByLabel("Password").fill(PASSPHRASE);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(
    page.getByRole("heading", { level: 1, name: "Your garden, at a glance." }),
  ).toBeVisible();
}

/** Seed source → draft profile → published version via the real API, using
 * the signed-in session cookie + CSRF token. Starter profiles are not
 * bootstrapped, so the journey authors its own. Weekly-days on today's
 * weekday puts the first occurrence on today. */
async function seedProfile(
  page: import("@playwright/test").Page,
): Promise<string> {
  return page.evaluate(
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
            title: "E2E care notes",
            citation: "E2E Owner Note, 2026",
            summary: "Journey-local care guidance.",
            retrievedOn: reviewedOn,
          },
        },
      );
      const srcProblems = src.createSource.problems as {
        code: string;
        message: string;
      }[];
      if (srcProblems.length) throw new Error(JSON.stringify(srcProblems));
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
            species: "E2E Monstera",
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
              {
                ruleKey: "inspect",
                kind: "CHECK",
                recurrence: { weeklyDays: { weekdays: [weekday] } },
                guidanceText: "Look over the leaves for pests before watering.",
                sourceVersionIds: [sourceVersionId],
              },
            ],
          },
        },
      );
      const draftProblems = draft.createCareProfileDraft.problems as {
        code: string;
        message: string;
      }[];
      if (draftProblems.length) throw new Error(JSON.stringify(draftProblems));
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
      const pubProblems = pub.publishCareProfile.problems as {
        code: string;
        message: string;
      }[];
      if (pubProblems.length) throw new Error(JSON.stringify(pubProblems));
      return pub.publishCareProfile.profile.currentVersion.id as string;
    },
    [TODAY_WD, iso(TODAY)],
  );
}

test("care journey: plan through the editor, act through the agenda", async ({
  page,
}) => {
  await signIn(page);
  await seedProfile(page);

  // ── own plant, then the Care tab starts honest: no plan, no dates ──────
  await page.goto("/plants/new");
  await page.getByLabel("Name", { exact: true }).fill("Care journey pothos");
  await page.getByRole("button", { name: "Add plant" }).click();
  await expect(page).toHaveURL(/\/plants\/[0-9a-f-]{36}$/);
  const detailUrl = page.url();

  await page.getByRole("tab", { name: "Care" }).click();
  await expect(page.getByText("No care plan yet")).toBeVisible();
  await page.getByRole("link", { name: "Create care plan" }).click();
  await expect(page).toHaveURL(/\/care\/plan\/[0-9a-f-]{36}$/);

  // ── editor: pin the reviewed profile, see rules with citations, and the
  //    server-computed next dates before anything is saved ────────────────
  await expect(
    page.getByRole("dialog", { name: "New care plan", exact: true }),
  ).toBeVisible();
  const profileValue = await page
    .locator("#pe-profile option", { hasText: "E2E Monstera · v1" })
    .getAttribute("value");
  await page.selectOption("#pe-profile", profileValue!);
  await expect(page.getByText(`Weekly on ${WD_SHORT}`)).toHaveCount(2);
  await expect(page.getByText("E2E Owner Note, 2026")).toHaveCount(2); // both rules cite it

  await page.getByRole("button", { name: "Preview next dates" }).click();
  await expect(
    page.getByText(
      `${day(0)} · ${day(7)} · ${day(14)} · ${day(21)} · ${day(28)}`,
    ),
  ).toHaveCount(2);

  await page.getByRole("button", { name: "Create plan" }).click();
  await expect(
    page.getByText("Plan created — the agenda fills from today."),
  ).toBeVisible();

  // ── the plant's Care tab now shows the pinned plan and its dates ───────
  await page.goto(detailUrl);
  await page.getByRole("tab", { name: "Care" }).click();
  await expect(
    page.getByText("Enabled", { exact: false }).first(),
  ).toBeVisible();
  await expect(page.getByText("E2E Owner Note, 2026")).toHaveCount(2); // both rules cite it
  await expect(page.getByText("Due " + day(0)).first()).toBeVisible();

  // ── agenda: today's action and check, plant named, kinds distinct ──────
  await page.goto("/care");
  const today = page.locator('section[aria-label="Today"]');
  // Match cards by their ruleKey <strong>: the inspect rule's guidance text
  // contains "watering", so plain hasText filters are ambiguous, and the
  // agenda's (due,id) sort makes same-day card order nondeterministic.
  const cardFor = (scope: Locator, ruleKey: string) =>
    scope.locator("li.card").filter({
      has: page.locator("strong.small", {
        hasText: new RegExp(`^${ruleKey}$`),
      }),
    });
  await expect(today).toBeVisible();
  await expect(today.getByText("Care journey pothos")).toHaveCount(2);
  await expect(today.getByText("water", { exact: true }).first()).toBeVisible();
  await expect(today.getByText("check", { exact: true })).toBeVisible();

  // ── complete the water action with quantity/unit/note ──────────────────
  const recordCare = page.getByRole("dialog", {
    name: "Record care",
    exact: true,
  });
  const snoozeCare = page.getByRole("dialog", {
    name: "Snooze care",
    exact: true,
  });
  const skipCare = page.getByRole("dialog", {
    name: "Skip occurrence",
    exact: true,
  });
  const waterCard = cardFor(today, "water");
  await waterCard.getByRole("button", { name: "Complete" }).click();
  await recordCare.getByLabel("Quantity (optional)").fill("500");
  await recordCare.getByLabel("Unit").selectOption("ML");
  await recordCare.getByLabel("Note (optional)").fill("Top 3cm dry");
  await recordCare.getByRole("button", { name: "Confirm" }).click();
  await expect(page.getByText("Completed.")).toBeVisible();
  await expect(today.getByText("water", { exact: true })).toHaveCount(0);

  // ── the check completes through its own verb, not an activity ──────────
  const checkCard = cardFor(today, "inspect");
  await checkCard.getByRole("button", { name: "Mark checked" }).click();
  await recordCare.getByRole("button", { name: "Confirm" }).click();
  await expect(
    page.getByText("Checked — logged to the journey."),
  ).toBeVisible();
  await expect(today).toHaveCount(0); // empty group disappears; overdue stays absent too

  // ── snooze an upcoming occurrence: original date stays visible ─────────
  const upcoming = page.locator('section[aria-label="Upcoming"]');
  const nextWater = cardFor(upcoming, "water").first();
  const nextWaterDate = day(7);
  await nextWater.getByRole("button", { name: "Snooze" }).click();
  await expect(snoozeCare.locator('input[type="date"]')).toHaveValue(day(8)); // default: next day
  await snoozeCare.locator('input[type="date"]').fill(day(9));
  await snoozeCare.getByRole("button", { name: "Confirm snooze" }).click();
  await expect(
    page.locator(".toast", { hasText: `Snoozed to ${day(9)}` }),
  ).toBeVisible();
  await expect(
    upcoming.getByText(`Snoozed to ${day(9)} · originally ${nextWaterDate}`),
  ).toBeVisible();

  // ── skip needs a confirm step and passes over only that one date ───────
  const nextCheck = cardFor(upcoming, "inspect").first();
  await nextCheck.getByRole("button", { name: "Skip" }).click();
  await expect(skipCare).toBeVisible();
  await skipCare.getByRole("button", { name: "Confirm skip" }).click();
  await expect(
    page.getByText("Skipped — the plan continues from the next date."),
  ).toBeVisible();

  // ── reload: the agenda state is the server's, not the client's ─────────
  await page.reload();
  await expect(today).toHaveCount(0);
  await expect(
    upcoming.getByText(`Snoozed to ${day(9)} · originally ${nextWaterDate}`),
  ).toBeVisible();

  // ── the journey keeps the care records, filterable ─────────────────────
  await page.goto(detailUrl);
  await page.getByRole("tab", { name: "Journey" }).click();
  await page.getByRole("button", { name: "Care", exact: true }).click();
  await expect(page.getByText("2 shown")).toBeVisible();

  // ── a11y on the agenda with real data ───────────────────────────────────
  await page.goto("/care");
  const results = await new AxeBuilder({ page }).include("main").analyze();
  expect(results.violations).toEqual([]);

  // ── leave the garden as we found it ─────────────────────────────────────
  // One shared disposable schema: the journey plant is archived so later
  // projects (capture, mobile auth) see no extra active plants, even if
  // project order changes.
  const plantId = new URL(detailUrl).pathname.split("/").pop()!;
  const archived = await page.evaluate(
    async ({ id }) => {
      const csrf = (await (await fetch("/api/auth/session")).json())
        .csrfToken as string;
      const gql = (query: string, variables: unknown) =>
        fetch("/api/graphql", {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
          body: JSON.stringify({ query, variables }),
        }).then((r) => r.json());
      const found = await gql(
        "query P($id: ID!) { plant(id: $id) { id revision } }",
        { id },
      );
      return gql(
        "mutation A($input: ArchivePlantInput!) { archivePlant(input: $input) { plant { id status } problems { code message } } }",
        { input: { id, expectedRevision: found.data?.plant?.revision ?? 1 } },
      ).then((r) => r.data?.archivePlant);
    },
    { id: plantId },
  );
  expect(archived?.problems).toEqual([]);
  expect(archived?.plant?.status).toBe("ARCHIVED");
});
