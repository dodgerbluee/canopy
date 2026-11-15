# PLAN01 — Planning contracts and storage foundation

Status: ACCEPTED. Implementation commit: 3079640cb4dce05f30d313e53b746aabc9a9374b.

Full GitHub CI passed on that commit:
https://github.com/dodgerbluee/canopy/actions/runs/43661976089

Added staged additive planning SDL, typed operations and three example responses.
Runtime server schema registration remains deferred to PLAN02, following the
existing explicit gqlgen schema-file convention. Existing live operations,
navigation and public MCP catalog remain unchanged; no placeholder endpoints.

Migration 19 creates plans, explicit-day actions, guidance snapshots and minimal
one-per-plan ownership markers. Database constraints preserve null unknowns,
user period precision, exact existing-money bounds, lifecycle metadata and
workspace/plan-scoped links. READY guidance content is immutable and accepted
provenance survives context generations. A real plant purge clears its reference
while retaining the marker to prevent resurrection. No actual plant details are
copied into fulfillment records.

Pure Go period validation accepts year/season/month/date intent without generating
fake days. Annual publication timing is modeled separately from year-specific user
intent. Quotes, relative timing and future projections remain distinct.

Executed local checks:

- All Go packages passed with a dedicated disposable PostgreSQL 17; includes
  existing database, collection, care, identity, insights, portability, HTTP,
  GraphQL, media and worker-related regressions plus the new planning tests.
- New tests cover valid/invalid precision shapes, leap dates, New Year, exact and
  unknown budgets, missing action dates, lifecycle metadata, cross-workspace and
  same-workspace/wrong-plan links, ownership without completing future work,
  purge markers, immutable/accepted guidance, workspace deletion and down/up
  preserving existing garden rows.
- The actual migrate CLI executed 19 -> 18 -> 19 in a second disposable database.
- Node24 contract validation/codegen, TypeScript check and Angular production
  build passed; three fixtures also passed actual GraphQL response completion.
- All generators ran, and make lint (gofmt/vet/TypeScript/Angular build) passed.
- make gen-check confirmed regeneration leaves no diff. Task dispatch, plan,
  packet and architecture boundary gates passed locally and in CI.
- GitHub CI passed release-tool checks, fresh dependency installation, generation
  no-diff, plan/boundary gates, lint, unit tests and real PostgreSQL integration.

No local or remote production database, provider, container or deployment was
changed. Only the explicitly named disposable test database was started and it
was removed after verification. The feature branch is feature/planting-planner;
it was not merged or published as a deployment image.
Planning writes remain inactive until PLAN02 adds real resolvers and populated
backup/export/restore support. Later scheduling/promotion commands will have
additive contract amendments rather than unfinished public handlers.

Next packet: PLAN02, domain/CRUD resolvers plus populated portability coverage.
The Planning page remains step 5; no visible UI is claimed in this foundation.

Raw logs, fixture checker and test migration executable remain ignored here.
