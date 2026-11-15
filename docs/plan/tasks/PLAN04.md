# PLAN04 — Planting schedule engine

Status: IN_PROGRESS.

### PLAN04 — Pure schedule projections and explicit confirmation

Depends: PLAN03.

Own: `server/internal/planning/`, `server/db/migrations/00021_planning_schedule.sql`, `server/db/queries/planning.sql`, `server/db/generated/` (generator only), `server/db/planning_test.go`, `contracts/graphql/planning.graphql`, `contracts/operations/planning.graphql`, `contracts/fixtures/api/planning-schedule-*.json`, `contracts/generated/` (generator only), `server/internal/transport/graphql/planning_resolvers.go`, `server/internal/transport/graphql/planning_schedule.go`, `server/internal/transport/graphql/planning_schedule_http_test.go`, `server/internal/transport/graphql/gql/` (generator only), `server/internal/transport/graphql/limits.go`, `server/internal/transport/graphql/limits_test.go`, `server/internal/portability/planning_test.go`, `docs/features/planting-planner.md`, `docs/plan/07-tasks.md`, `docs/plan/tasks/PLAN04.md`, `docs/plan/tasks/PLAN04-contract-change.md`, `.artifacts/agents/PLAN04/`. Integrator owns graph/ledger.

Acceptance: Add a pure deterministic schedule engine, a scoped paginated per-plan projection query for at most twelve civil months, and a transactional explicit source-backed period confirmation. Preserve coarse precision, distinct/cross-year windows, source hemisphere, leap-day uncertainty, signed relative offsets, missing anchors and unknown first harvest. Display bands are approximate rendering bounds, never asserted action days. Use workspace-local today and injected time; projection reads never scrape, call AI or write. Validate current READY evidence, generation, method, context and chosen-period containment before accepting provenance; preserve user intent, actions and previous choices on failures/refresh. Retain confirmation inputs/source precision in nullable portable selection metadata. No UI, reminders, garden promotion, MCP or runtime/production changes.

Verify: Pure calendar/anchor/precision/property tests; real PostgreSQL scope, context/generation conflicts, concurrent confirmation/replay, audit rollback, manual/failed paths and immutable source history; actual frozen HTTP operations and request cost bounds; populated/pre-column backup/export/restore; migration up/down/reapply; full Go regression, race detector, Node24 contracts/generation/build, plan/boundary gates and GitHub CI.
