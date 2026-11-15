# PLAN07 — Atomic Add to garden and action completion

Status: ACCEPTED.

### PLAN07 — Add to garden and explicit action completion

Depends: PLAN06.

Own: `Makefile`, `server/internal/planning/`, `server/internal/planninggarden/`, `server/internal/collection/commands/`, `server/internal/collection/plants.go`, `server/db/queries/planning.sql`, `server/db/generated/` (generator only), `server/internal/transport/graphql/planning_garden.go`, `server/internal/transport/graphql/planning_garden_http_test.go`, `server/internal/transport/graphql/collection_resolvers.go`, `server/internal/transport/graphql/resolver.go`, `server/internal/transport/graphql/gql/` (generator only), `contracts/graphql/planning.graphql`, `contracts/operations/planning.graphql`, `contracts/generated/` (generator only), `server/internal/portability/planning_test.go`, `web/src/app/features/planning/`, `web/e2e/planning*.ts`, `web/playwright.planning.config.ts`, `docs/features/planting-planner.md`, `docs/plan/07-tasks.md`, `docs/plan/tasks/PLAN07.md`, `docs/plan/tasks/PLAN07-contract-change.md`, `.artifacts/agents/PLAN07/`. Integrator owns graph/ledger.

Acceptance: Atomically create or link one actual living plant/group using existing collection validation, acquisition/purchase/context/audit rules and an explicit narrow transaction interface. Review actual count, dates, current placement and optional real price independently of intent. No budget copying or future placement/date assertions. Fulfillment is distinct from terminal completion; buying early leaves future plant-out work open. Explicit action completion/dismissal uses real civil dates, requires a living fulfillment for sow/plant actions and records actual plant date/context through collection rules in the same transaction. One link forever, purge-safe receipt replay, scoped/revision/audit/idempotency guards and no half writes. Shared modal UI with protected drafts, native choices and honest errors.

Verify: Real PostgreSQL/HTTP atomicity, rollback, concurrent different keys, lost-response replay, stale/cross-workspace input, actual unknown count, linking/purge tombstones, buying early, completing planting later and independent plan completion; populated portability. Angular unit/build and actual desktop/phone browser journey. Full Go regression/race, generation/lint/plan/boundary, matched preview preservation/queued jobs and full CI.
