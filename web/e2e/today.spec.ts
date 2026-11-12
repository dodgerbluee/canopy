import { expect, test, type Locator } from "@playwright/test";

/** T28 acceptance journey: the Today dashboard composes due work, journal
 * lines and conditions from ONE bounded read. Seed a plan through the API,
 * complete a real occurrence through the quick action, and watch the plant's
 * journal tab show the atomically created activity — then the dashboard's
 * own journey card reflects it. Also pins the initial-load budget: the
 * dashboard is one composition, not a query scatter. Runs after the
 * empty-state specs because it creates plants (see playwright.e2e.config.ts). */

const PASSPHRASE = process.env.E2E_PASSPHRASE ?? "e2e-dev-passphrase";

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

async function signIn(page: import("@playwright/test").Page): Promise<void> {
  await page.goto("/");
  await page.getByLabel("Password").fill(PASSPHRASE);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(
    page.getByRole("heading", { level: 1, name: "Your garden, at a glance." }),
  ).toBeVisible();
}

/** Seed source → published profile (daily water ACTION + daily inspect
 * CHECK) → plant → plan, all through the real API on the signed-in session.
 * Returns the plant id for the journal-tab leg. */
async function seedPlan(
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
      const probs = (v: any) => {
        const p = (v.problems ?? []) as { code: string; message: string }[];
        if (p.length) throw new Error(JSON.stringify(p));
      };

      const src = await gql(
        `mutation ($input: CreateSourceInput!) {
          createSource(input: $input) { source { currentVersion { id } } problems { code message } }
        }`,
        {
          input: {
            kind: "OWNER_NOTE",
            title: "E2E today notes",
            citation: "E2E Owner Note, 2026",
            summary: "Journey-local care guidance.",
            retrievedOn: reviewedOn,
          },
        },
      );
      probs(src.createSource);
      const versionId = src.createSource.source.currentVersion.id as string;

      const draft = await gql(
        `mutation ($input: CreateCareProfileDraftInput!) {
          createCareProfileDraft(input: $input) { profile { id revision } problems { code field message } }
        }`,
        {
          input: {
            category: "OTHER",
            species: "E2E Today Fern",
            environment: { situations: ["INDOOR"], hemispheres: ["NORTHERN"] },
            rules: [
              {
                ruleKey: "water",
                kind: "ACTION",
                activityKind: "WATER",
                recurrence: { weeklyDays: { weekdays: [weekday] } },
                guidanceText: "Keep evenly moist, never soggy.",
                sourceVersionIds: [versionId],
              },
              {
                ruleKey: "inspect",
                kind: "CHECK",
                recurrence: { weeklyDays: { weekdays: [weekday] } },
                guidanceText: "Check frond undersides for scale.",
                sourceVersionIds: [versionId],
              },
            ],
          },
        },
      );
      probs(draft.createCareProfileDraft);
      const profile = draft.createCareProfileDraft.profile as {
        id: string;
        revision: number;
      };

      const pub = await gql(
        `mutation ($input: PublishCareProfileInput!) {
          publishCareProfile(input: $input) { profile { currentVersion { id } } problems { code field message } }
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
      probs(pub.publishCareProfile);

      const plant = await gql(
        `mutation ($input: CreatePlantInput!) {
          createPlant(input: $input) { plant { id } problems { code message } }
        }`,
        {
          input: {
            name: "Today journey fern",
            type: "INDIVIDUAL",
            category: "OTHER",
          },
        },
      );
      probs(plant.createPlant);
      const plantId = plant.createPlant.plant.id as string;

      const plan = await gql(
        `mutation ($input: CreateCarePlanInput!) {
          createCarePlan(input: $input) { plan { id } problems { code field message } }
        }`,
        {
          input: {
            plantId,
            profileVersionId: pub.publishCareProfile.profile.currentVersion.id,
            timezone: "UTC",
          },
        },
      );
      probs(plan.createCarePlan);
      return plantId;
    },
    [TODAY_WD, iso(TODAY)],
  );
}

// Due groups are <li class="due-group"> inside the "A little attention" card;
// each group is pinned by its own heading (Today / Upcoming).
const groupCard = (
  page: import("@playwright/test").Page,
  label: string,
): Locator =>
  page
    .locator("li.due-group")
    .filter({ has: page.getByRole("heading", { name: label, exact: true }) });

// One demand per .due-item, pinned by its rule key.
const cardFor = (
  page: import("@playwright/test").Page,
  scope: Locator,
  ruleKey: string,
): Locator =>
  scope.locator(".due-item").filter({
    has: page.locator("strong.small", { hasText: new RegExp(`^${ruleKey}$`) }),
  });

test("today journey: quick-complete keeps dashboard and journal coherent", async ({
  page,
}) => {
  await signIn(page);
  const plantId = await seedPlan(page);

  // ── the dashboard shows today's work and says honestly it has no location ─
  await page.goto("/");
  const today = groupCard(page, "Today");
  await expect(today).toBeVisible();
  await expect(today.getByText("Today journey fern")).toHaveCount(2);
  await expect(
    page.getByText("No location with coordinates yet"),
  ).toBeVisible();

  // ── quick complete: WATER demands a quantity — the form never guesses one ─
  const recordCare = page.getByRole("dialog", {
    name: "Record care",
    exact: true,
  });
  const snoozeCare = page.getByRole("dialog", {
    name: "Snooze care",
    exact: true,
  });
  const water = cardFor(page, today, "water");
  await water.getByRole("button", { name: "Complete" }).click();
  await recordCare.getByLabel("Quantity").fill("500");
  await recordCare.getByLabel("Unit").selectOption("ML");
  await recordCare.getByRole("button", { name: "Confirm" }).click();
  await expect(
    page.getByText("Completed — logged to the journey."),
  ).toBeVisible();
  await expect(cardFor(page, groupCard(page, "Today"), "water")).toHaveCount(0);

  // ── snooze moves the check to Upcoming and keeps the original date on it ──
  const inspect = cardFor(page, groupCard(page, "Today"), "inspect");
  await inspect.getByRole("button", { name: "Snooze" }).click();
  await snoozeCare.getByLabel("Snooze until").fill(day(1));
  await snoozeCare.getByRole("button", { name: "Confirm" }).click();
  await expect(
    page.getByText("the original date stays on the card"),
  ).toBeVisible();
  const upcoming = groupCard(page, "Upcoming");
  // next week's inspect also lives in Upcoming: pin the snoozed card by its
  // honest note — the original date stays on it.
  const snoozedCard = upcoming
    .locator(".due-item")
    .filter({ hasText: `Snoozed to ${day(1)} · originally ${day(0)}` });
  await expect(snoozedCard).toBeVisible();

  // ── the plant's journal tab shows the atomically created activity ────────
  await page.goto(`/plants/${plantId}`);
  await page.getByRole("tab", { name: "Journey" }).click();
  await expect(
    page.locator("li.card", { hasText: "Watered" }).first(),
  ).toBeVisible();

  // ── and the dashboard's journey card shows the same line ──────────────────
  await page.goto("/");
  const journey = page
    .locator("section.card")
    .filter({ has: page.getByRole("heading", { name: "Recent journey" }) });
  await expect(journey.getByText("Watered").first()).toBeVisible();
});

test("initial dashboard load stays within the composition budget", async ({
  page,
}) => {
  let posts = 0;
  page.on("request", (r) => {
    if (r.method() === "POST" && new URL(r.url()).pathname === "/api/graphql")
      posts++;
  });
  await page.goto("/");
  await page.getByLabel("Password").fill(PASSPHRASE);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(
    page.getByRole("heading", { level: 1, name: "Your garden, at a glance." }),
  ).toBeVisible();
  await page.waitForLoadState("networkidle");
  // todayOverview + plant names + the T34 suggestions slot (open
  // suggestions + engine capabilities): the dashboard is ONE composition,
  // and the budget catches any future query scatter.
  expect(posts).toBeLessThanOrEqual(5);
});
