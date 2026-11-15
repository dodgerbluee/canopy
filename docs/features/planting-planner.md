# Future planting planner

Status: steps 1–7 of 8 accepted, including the Planning page and shared modal flows. An isolated Docker preview is available at
http://127.0.0.1:7082/planning. Calendar/Year/Today integrations and opt-in reminders are implemented in step 6;
atomic garden promotion and action completion are implemented in step 7;
agent/assistant work follows in step 8; the user authorized continuing through all.
Existing production deployments are unchanged.

## Product direction

Add **Planning** at `/planning`: decide what to grow, where it might go, and when
to start. Keep an idea with only a name, develop it into a dated plan, and turn
it into an actual plant or group when the material is acquired or growing.

The page answers four questions:

1. What would I like to grow?
2. Which planting opportunities apply to my place and starting method?
3. What have I chosen to do, and what comes next?
4. When I actually start, how do I carry the information into my garden?

V1 covers future trees, shrubs, berry patches and annual crops. It is a planning
tool with a wishlist and schedule; a spatial garden-bed designer is a later feature.

## What exists, and what must change

Reviewed against the current source:

| Existing capability | Reuse or gap |
| --- | --- |
| `/year`, per-plant tracks and merged garden overview | Reuse the month-scale/segment presentation; keep future plans identifiable. |
| Regional, cited knowledge and configured AI connections | Reuse fetching, source validation, model routing and provenance. |
| `PlantKnowledge`, enrichment worker and `askGarden` | Keep the owned-plant adapter. PLAN03 adds crop-based research without an existing plant ID; planning chat context follows in PLAN08. |
| Seasonal kinds | Existing growth/bloom/harvest/care remain compatible; PLAN03 adds separate seed-starting, direct sowing and transplant facts. |
| Crop matching and source registry | Eight crop families and twelve regional publications after PLAN03, including reviewed Texas/Oregon tomato and UK strawberry sources; this is not a universal planting catalog. |
| Garden geography | Explicit country/region/place, optional coordinates and zone; timezone does not imply climate. |
| Calendar | Existing events are day-based and mostly reference owned plants. Planning needs a separate projection. |
| Collection creation | Existing validated, audited, idempotent mutation; promotion must share its transaction. |
| Backup/export | Explicit table allowlists; new planning records must be added, not assumed to be included. |

Relevant files: `contracts/graphql/{collection,knowledge,calendar,schema}.graphql`,
`server/internal/{knowledge,collection,portability}/`,
`web/src/app/features/{seasons,calendar}/` and `web/src/app/shell/`.

## Page and interactions

Desktop: a quiet header, a compact next-action summary, and aligned plant rows
beside a twelve-month timeline. Use the rounded bands and restrained colors from
Garden year. The timeline defaults to the coming twelve months, with an explicit
calendar-year option; show year labels when the range crosses New Year.

```text
PLANNING                                           Plan a planting
Your next growing season.                    < Nov 2026–Oct 2027 >

[Planned] [Wishlist]                                  Search  Filters

Plant / intended location        Nov Dec Jan Feb Mar Apr ...
Blueberry · 2 plants             [window to confirm]
Another fig · container                   [planting window]
Strawberry patch · garden bed                 [sow]—[grow]—[harvest?]

No date yet
Ideas can stay here until you are ready to schedule them.
```

This is a layout illustration, not planting advice or a promised crop calendar.
Windows shown in the product must come from the user or applicable evidence.

- Each row shows plant/cultivar, intended location, starting method and one short
  next step. Clicking it opens a consistent detail modal. Editing uses a pencil
  in its heading; cancel/remove actions live inside Edit.
- Planned and Wishlist are the primary views. Completed/cancelled items are
  available through the hidden filter panel. No large permanent filter form.
- The primary action opens the shared modal pattern. Required initially: plant
  name only. Cultivar, quantity, starting method, place, target period, budget and
  notes are optional or progressive sections. Keep native choice fields where
  appropriate. No validation errors on a fresh untouched form.
- Plant lookup offers known crops and a manual entry. Unrecognized species can
  be saved; absence of guidance is not a form error.
- A source explanation is a short **Why this window?** expansion with publisher,
  region, supporting conditions and retrieval date. Keep extraction internals
  out of the normal page.
- Mobile uses a full-width chronological list with compact month summaries.
  Opening a plan uses the established full-screen modal/sheet and a clear Back
  action. A wide timeline is optional, not the only way to use the page.
- Add a desktop navigation link near Garden year and expose it under mobile
  More. Keep the existing mobile bottom bar stable.
- Use existing spacing/type/field/dialog tokens, light/dark themes and icon
  primitives. Target 24px desktop and 16px mobile section spacing, with at least
  44px interactive targets and comfortable modal scrolling.

## Main flows

**Save an idea.** Enter a plant, optionally a cultivar and notes. Save to Wishlist
without needing a date, location, provider or botanical catalog match.

**Develop a plan.** Choose a starting method: start seeds indoors, sow directly,
or start with a nursery plant. Select a place/placement and future period. Ask for
available guidance or enter personal target dates. Compare suggestions, then save
the intended window. A coarse window remains coarse.

**Choose actions.** A window can stay on the planning timeline without a specific
day. The user may pin explicit dates for buying, preparing, sowing or planting,
and opt into in-app reminders. Only those selected dates become calendar markers.

**Start growing.** Open **Add to garden**, review a prefilled version of the
existing plant form, and confirm what actually happened. Name, species, cultivar,
location and placement carry forward. Acquisition date, planting date, actual
count and actual price are confirmed independently of the plan.
The form distinguishes actual current placement from the intended future site;
buying early must not record a future ground context or planting date as true.
The user can also link a plant already added through the collection. Buying a
nursery tree in advance starts ownership, while its future plant-out action stays
open. Linking/creating a plant does not complete the whole plan.

## Model and invariants

Use separate future-planning records; do not add a future status to `plants` or
create placeholder owned plants to obtain knowledge.

Proposed records:

- `planting_plans`: workspace, name, optional scientific identity/cultivar/catalog
  reference, optional desired quantity, starting method, intended location and
  placement, target period, optional budget/currency, notes, state and revision.
- `planting_actions`: plan, action kind, explicitly chosen local date, reminder
  preference, state and schedule revision. Child edits use the plan aggregate revision;
  these are planning actions, not care occurrences.
- `planting_guidance_snapshots`: plan/context identity, provider/source versions,
  supported windows, assumptions, provenance and retrieval state.
- `planting_fulfillments`: minimal immutable link from a plan to its resulting
  actual plant/group. Actual plant details stay in the collection and audit
  records rather than being copied into planning data.

Logical states: `IDEA → PLANNED → IN_PROGRESS → COMPLETED`, with `CANCELLED` for
abandoned work. Ownership is a separate fulfillment link, not a terminal planning
state. Active plans can be revised; cancelled items can be restored. Completed
items retain provenance and can be duplicated for another season without copying
fulfillment links or completed actions. Cancelling or hiding a plan never archives
or deletes a linked actual plant.
Upcoming, in-window and window-passed labels are derived from dates, not lifecycle
states. A passed window prompts review; it does not cancel or roll the plan forward.

V1 has one fulfillment per plan: one individual plant or one tracked group.
Multiple individually tracked plants, partial fulfillments, seed inventory and
automated succession series are later extensions. The conversion dialog must
make this boundary clear; users can split ideas into separate plans first.
Planned quantity is an intention, not a claim about germination or surviving plants.
Buying a seed packet does not create an actual plant. Sowing a batch or acquiring
a living nursery plant can use Add to garden, with actual count left unknown when
appropriate. Completing the planting action uses the same promotion transaction.

Keep crop identity separate from the existing coarse `PlantCategory` enum. A new
species can remain manual and later convert with category `OTHER`; do not add an
enum value for every crop or lose its scientific name/cultivar in that mapping.

Unknown values remain null. Budgets are estimates, stored in exact minor units
with their currency; they never create purchases or enter actual spending totals.
Actual purchase linkage uses the existing acquisition/purchase flow only when
the user supplies real purchase information.

All records are workspace scoped, revision controlled and audited. Retriable
mutations use `clientMutationId` with existing conflict/replay semantics. Add
planning data to backup/restore, exports and workspace deletion; retained links
to purged plants must be tombstoned/cleared according to the existing privacy
rules, never retain a hidden copy of erased plant details.

## Guidance and scheduling

Separate three concepts in the API and UI:

1. A publication's recommendation, including its conditions and precision.
2. The user's chosen target window/action dates.
3. Estimated future growth/harvest, where enough information supports it.

The scheduler is a pure Go component receiving explicit context, immutable facts,
year/range and clock. It does not scrape, call an LLM or write records. Preserve
day/month/season/year/relative/unknown precision. Never turn a season into an asserted
exact day or pick January 1 because only a year is known.

Chosen dates are civil dates in the workspace timezone, not the browser's zone or
UTC instants. Reminder instants follow existing DST resolution rules. A timezone
change preserves chosen calendar days and previews reminder recalculation.

Support more than one eligible window, cross-year ranges and source hemisphere.
Preserve complete timing clauses and cultivar distinctions, applying the lessons
from the previous harvest-ingestion fix. A window end is not a biological deadline.
Relative advice needs a supported anchor; a maturity interval measured from
transplanting cannot be applied to a sowing date.
Advice relative to frost stays relative until an applicable frost anchor exists;
neither timezone nor a hardiness-zone label is a substitute.

Use confirmed geography and source applicability. Hardiness zone can inform a
suitability review but does not supply a planting date by itself. Keep indoor,
container and ground conditions distinct. A nursery fruit tree's first harvest
is unknown without appropriate age/cultivar/source context; do not automatically
show a same-year crop. Long-range plans use applicable seasonal/climate guidance,
not a claim that a weather forecast exists for that future date.

Generalize knowledge retrieval around crop identity and regional context so it
can serve a future plan without a `plant_id`. Preserve the current owned-plant
adapter. Use workspace-scoped generation/cache identities including taxon,
cultivar, method, placement, geography revision, source/extraction versions and
relevant year. Reuse applicable source fetches without exposing private context
between workspaces.
Cache year-independent publication extraction independently of yearly schedule
projections; changing the displayed year should not trigger another model call.

Extend the existing curated source registry through reviewed adapters. Current
coverage should be labelled honestly; a new crop/region can remain manual until
an applicable publication is supported. Use bounded, deduplicated jobs and provider
usage limits so new ideas do not starve existing-plant processing. Unsupported, missing-location, disabled,
processing and failed states must all leave manual planning available.

Source/model refresh produces a new draft, not a silent reschedule. Keep accepted
dates and their original evidence snapshot. Show a compact update notice and let
the user preview changes. Changing an identity or location invalidates mismatched
guidance and pending old-generation jobs. User dates remain explicitly theirs.

## AI behavior

Reuse the configured assistant connection/model routing. No extra AI service or
credentials are required. **Help me plan** can summarize applicable windows,
explain missing context and propose form values. Validate cited fact IDs and
returned fields before showing a draft. Applying a draft fills the modal; saving
still requires the user's normal confirmation.

Planning context is explicit: current garden, proposed additions, intended space,
known constraints and cited guidance are separate inputs. Existing `askGarden`
must not start describing future plans as already-owned plants. Manual planning
and deterministic timing work without AI. Provider failure or budget exhaustion
cannot lose a saved plan.

## Integration boundaries

- **Calendar:** add a typed planning projection/query rather than inventing
  plant IDs or altering recorded activity history. Only explicit confirmed action
  days appear as planning markers. Add a separate Planned plantings filter group;
  month/season windows stay as bands in Planning and the optional Year overlay.
  Retain the existing 62-day calendar request limit. The separate timeline query
  is bounded to a twelve-month view, paginated as needed, and matches overlapping
  windows even when their start lies outside the visible range.
- **Garden year:** an optional Include planned plantings overlay uses dashed
  bands/clear labels. Do not add estimates to actual garden event counts.
- **Today:** a small Upcoming plantings section uses confirmed planning actions.
  Care remains focused on real plants and their actual care plans.
- **Inbox:** opt-in planning reminders use the existing worker/notification path,
  deduplicated by action and schedule revision. Rescheduling/cancelling invalidates
  outstanding reminders, preserving notification history and explicit read state.
  Process only a bounded near-term horizon, not every day of every future year.
- **Purchases:** planned budget and seller notes remain estimates. A later receipt
  is recorded through existing real-purchase/accounting semantics.
- **MCP:** provide typed planner read/write tools through the current catalog and
  credential/scoping model. Adding to the real garden also requires existing
  collection-write permission. AI output never bypasses command authorization.

No new database, queue service or vector store: retain the current app + PostgreSQL
deployment and existing River worker.

## Transactional Add to garden

One command must atomically validate/lock the plan, create the real plant through
the existing collection rules (or validate a selected existing plant), create
context/acquisition/audit records where actually supplied, write the fulfillment
link and update planning progress without closing remaining future actions. Use a
narrow collection transaction interface; do not call public GraphQL internally or
make the frontend save a plant and then separately link it to the plan.

Expected revision plus an idempotency key prevents double clicks, ambiguous
network retries and concurrent clients from producing duplicate plants. A
different request after fulfillment returns the existing link/conflict rather
than creating another plant. A failed transaction leaves neither half behind.
Subsequent edits to the actual plant do not rewrite the original planning record.
For an already linked plant, a later planting action records confirmed actual
context/date changes through existing collection/journal rules. It creates no new
plant. Completing the overall plan is distinct from ownership and an elapsed date.

## Eight sequential implementation packets

PLAN01 through PLAN07 are accepted.
Register the remaining sequential implementation packets when they are kicked off. The
accepted base contract is recorded in `docs/plan/tasks/PLAN01-contract-change.md`;
PLAN03's additive refresh/research contract is recorded in
`docs/plan/tasks/PLAN03-contract-change.md`. UI navigation arrives in PLAN05.

| Step | Deliverable | Main paths | Acceptance/verification |
| --- | --- | --- | --- |
| 1 — complete | Contracts and storage | Accepted contract change record; `contracts/graphql/planning.graphql`, operations, migration 00019 | States, date precision, source vs user dates, permissions, pagination, revisions and promotion semantics defined. Disposable PostgreSQL up/down/reapply, generators, local regression suite and full GitHub CI passed. |
| 2 — complete | Planning domain and CRUD | `server/internal/planning/`, DB queries, GraphQL resolver, portability manifests | Workspace isolation, optional values, exact budgets, transactional revisions/audit/replay, scoped cursors and complete backup/export inclusion implemented. Real HTTP operations, populated and legacy restore, race checks, full backend regression and GitHub CI passed. |
| 3 — complete | Crop-based guidance | `server/internal/knowledge/`, source adapters, enrichment queries/worker | Research before a plant exists, automatic/explicit refresh, immutable provenance, honest timing, stale-generation fencing, public cache and bounded provider usage. Source/cultivar/region/method matching, three configured transports, failed/manual paths, populated/legacy portability, race checks, fresh primary-source/local-model smoke and full GitHub CI passed. |
| 4 — complete | Schedule engine | `server/internal/planning/schedule.go` and scoped query/confirmation commands | Pure civil projections, twelve-month per-plan pagination and explicit acceptance with portable assumptions. Distinct/overlapping windows, Gregorian leap cycle, hemispheres, missing/wrong anchors, first-harvest uncertainty, conflicts/replay/rollback and preserved choices verified. Populated/legacy restore, migration CLI 21→20→21, race checks, full Go regression and GitHub CI passed. |
| 5 — complete | Angular page and shared modals | `web/src/app/features/planning/`, data layer, routes/shell/More, batched board composition | Complete ideas/plans/edit/actions/source-choice flows on desktop/phone with shared modal guards, hidden URL filters and both themes. 504 Angular tests/build, real API/race/regression checks, 20 authenticated browser/accessibility scenarios and paired Docker preview and full GitHub CI passed. |
| 6 — complete | Calendar, Year, Today and reminders | Existing calendar/seasons/today/inbox modules; scoped planner projections and worker | Planning stays visually distinct and filterable, with no fake real-plant events. Only explicit dates create markers/reminders. Separate explicit-day projection, optional Year overlay, Today actions and opt-in in-app Inbox reminders verified with scope/date/cancellation/concurrency/timezone tests, 32 browser scenarios, full Go regression and GitHub CI. |
| 7 — complete | Add to garden | Planning promotion command, narrow collection transaction interface, existing plant form | One atomic conversion to one plant/group, with confirmed actual values and origin link. Actual create/link, acquisition-only and sow/plant completion flows implemented. Lost-response retry, 12 competing clients, full audit rollback, purge replay, real/unknown price/count, early acquisition then later planting and populated restore pass; full GitHub CI passed. |
| 8 | Agent integration and release gate | Agent catalog/context, remaining browser/portability tests, release/deployment tooling | Scoped MCP read/write/promote behavior, complete round-trip portability, full end-to-end journey and no planning leakage into actual totals. Verify deployed API/worker compatibility and queued jobs before release. |

Run one packet at a time. Each finishes with evidence, tests appropriate to the
business behavior, and a clean handoff before the next model starts.

## Release acceptance

A user can save a future crop they do not own; view its supported/unknown planting
windows; select their own dates; see explicit planning markers/reminders; and
later add the actual plant/group once. Guidance changes never overwrite accepted
choices. Existing plants, photos, calendar history, spending and care remain
correct. No-location, no-provider and unsupported-region users still have a
useful manual planner. Backup/restore retains plans, citations, actions and links.

Defer spatial bed maps, seed inventory, partial fulfillment, bulk individual-plant
creation, automated succession runs and optimizer-generated garden layouts.
