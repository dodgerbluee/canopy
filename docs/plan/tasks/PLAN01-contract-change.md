# PLAN01 — Additive planting contract

Accepted for the user-authorized first implementation step ("lets start"),
following PLAN00. This adds a staged contract; it changes no existing operation,
scalar, event kind, plant category or live resolver. Server SDL registration is
reserved for PLAN02, together with real resolvers and portability coverage.

## Aggregate, defaults and unknowns

`PlantingPlan` owns actions, guidance references and at most one fulfillment.
All writes scope by the authenticated workspace; ownership of a related location,
plant, action or snapshot is checked within that same workspace and plan.

Only name is required to save an idea. Unknown quantity, method, placement,
location, budget and target remain null. Names/identity labels are trimmed and
1..160 Unicode characters when supplied; notes are at most 20,000 characters.
Create accepts only IDEA (default) or PLANNED. Category defaults to OTHER without
losing the independent scientific name/cultivar.

Update is a full replacement of editable intent, not a patch. Omitted/null optional
fields clear their values. Only IDEA, PLANNED and IN_PROGRESS are editable.
Actions and ownership never change as a side effect of editing intent. A changed
period detaches the accepted-guidance pointer, retaining the historical snapshot;
other context edits invalidate enrichment generation without changing user dates.

A budget uses the existing `money.Parse`/`money.ValidCurrency` rules: canonical
nonnegative digit string <= 10^15 minor units, uppercase three-letter currency,
no conversion. Both budget columns are null together or present together. Zero is
an explicit estimate of free. Estimates never enter the actual spending query.

## Progress is not ownership

Legal explicit state changes:

- IDEA -> PLANNED or IN_PROGRESS.
- PLANNED -> IDEA or IN_PROGRESS.
- IN_PROGRESS -> COMPLETED, only when no OPEN actions remain.
- The same requested state is a no-op after revision validation.
- COMPLETED is terminal in V1; duplicate intent for another season instead.
- Cancel is a dedicated command for IDEA/PLANNED/IN_PROGRESS, recording the prior
  state. Restore is a dedicated command back to that prior state. Cancelled items
  cannot be edited until restored. Neither command changes an actual plant.

Dates never advance state automatically. Completion is a user's planning-progress
assertion; it creates no planting/watering/purchase activity by itself. Later
real-action completion commands are separate additions in PLAN07 and use actual
confirmed dates/context through collection/journal rules.

A fulfillment is a minimal immutable ownership link (created or existing), not
an actual-plant input snapshot. A newly acquired nursery plant may coexist with
an OPEN future PLANT_OUT action. Linking never silently completes that action or
sets an unconfirmed planting date/current ground context. The command may mark
progress IN_PROGRESS but never COMPLETED merely because a link exists.

A plant purge clears only `plant_id` through its foreign key. The marker survives
with `plant: null`, so a later retry cannot create a replacement plant under the
old plan. No actual plant details are duplicated into this record. PLAN07 must
return RESOURCE_GONE for that marker; another explicit plan is needed for a new
plant. Multiple plans may reference the same existing plant; each plan has only
one link. Deleting an action clears its optional link reference.

## Civil target periods

`targetPeriod: null` means no chosen period. Exactly one body must match precision:

| Precision | Required body | Stored meaning |
| --- | --- | --- |
| DATE_RANGE | dates.from/to, strict LocalDate | Inclusive real days, start <= end. |
| MONTH_RANGE | months.from/to, strict YYYY-MM | Inclusive months; no invented day. |
| SEASON | season.season/year | Abstract local season and its starting year. |
| YEAR | year | That year only; no January 1 date is recorded. |

Years are 1..9999, matching LocalDate. Mixed bodies, overflow dates/months,
reversed bounds and infinite DB dates are invalid. A season requires applicable
geography before it can be projected; timezone never supplies hemisphere. For a
season crossing New Year, the supplied year is its starting year (Northern
WINTER 2027 starts in December 2027; a projection is still approximate).

An action always has an explicit selected `scheduledOn` civil day. Optional
`reminderOn` must not be later; delivery defaults to 09:00 in workspace timezone,
following the existing DST convention. `completedOn` is actual confirmation,
not a copy of scheduledOn. DONE requires actual completion metadata; OPEN and
DISMISSED have none. The completion commands arrive with their domain behavior,
not as a generic status toggle in this step.

## Guidance provenance

Recommendations and user periods are separate. Year-specific absolute
recommendations reuse a precision-preserving period. Recurring publication
windows use ANNUAL with year-free month/day, month range or season; no model call
assigns a future year merely to store a source fact. Months are 1..12, and an end
before a start wraps New Year. Annual February 29 is representable; a non-leap
projection requires review rather than silently inventing February 28.
Relative recommendations retain an anchor and signed
optional day offsets. Null offsets are unknown, not zero. Unsupported timing has
no fake bounds. Multiple distinct windows remain distinct.

Snapshots are scoped by workspace/plan/generation. READY content is immutable;
a fresh generation does not update its predecessor or accepted user dates.
The accepted reference must belong to the same plan. Deferred checking allows a
whole plan and its snapshots to be deleted together without breaking the cycle.
PLAN04's confirmation command must additionally require READY source-validated
facts, matching context/generation and the user's selected period. No CRUD input
can set the accepted pointer directly.

Facts are typed at the API boundary. JSONB storage checks bounded array shape;
PLAN03 validates every nested citation, phase, condition and timing claim before
finishing a snapshot. It must not treat shape validation as evidence validation.
latestGuidance returns only the current guidanceGeneration; it never substitutes
an older READY row. acceptedGuidance can be older, which the UI detects through
the separate generation value while keeping the accepted dates intact.

## Concurrency, pagination and permissions

Use existing clientMutationId/request-hash receipts. Replays with the same input
return their prior result; changed input under the same key is
IDEMPOTENCY_CONFLICT; erased results are RESOURCE_GONE. A stale expectedRevision
is CONFLICT/currentRevision and writes nothing.

The plan is the revision aggregate. Every modifying child command also locks and
increments its parent's revision; action edits carry the parent's expectedRevision.
At most 100 actions per plan. scheduleRevision changes only when kind, selected
day, reminder or reminder eligibility changes, enabling stable notification dedup.
DONE/DISMISSED actions are not edited or deleted through the OPEN-action CRUD API.

List first is 1..100 (default 25), order NAME_ASC or UPDATED_DESC, stable id tie
breakers. NAME_ASC uses lower(name) with C collation. Cursors are workspace/filter/
order bound using the existing cursor pattern. A missing or other-workspace id
is not enumerable. Default states are IDEA/PLANNED/IN_PROGRESS; an explicitly
empty state list matches nothing. Search is a trimmed substring of name,
scientific name and cultivar, at most 200 characters.

Existing garden-member read/write rules apply. Future agent tools must explicitly
register planner permissions; creating/changing an actual plant additionally
requires existing collection-write authority. No new MCP tool is exposed here.

## Activation and portability handoff

Migration 19 is additive and reversible; tests run only on disposable PostgreSQL.
The running local/remote images and databases are not upgraded in PLAN01.
contracts codegen consumes planning.graphql now, following the project's staged
contract convention. server/gqlgen.yml does not consume it until PLAN02 can supply
real resolvers. No public panic/not-implemented endpoints or navigation are added.

Before activating API writes, PLAN02 must extend backup/export/restore allowlists
with all four tables. Restore order must handle the accepted-snapshot cycle:
insert plans without accepted_guidance_id, restore children, then restore the
validated accepted pointers in the same transaction (or defer the FK for the
entire restore transaction). A populated planner round-trip is mandatory.
Future schedule, source-refresh and garden-promotion commands will get additive
contract amendments with the relevant implementation packets rather than public
placeholder mutations now.
