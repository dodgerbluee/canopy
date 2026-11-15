# PLAN01 — Contracts and storage

Status: ACCEPTED. Evidence: `.artifacts/agents/PLAN01/EVIDENCE.md`.

### PLAN01 — Planting contracts and storage foundation

Depends: PLAN00.

Own: `contracts/graphql/planning.graphql`, `contracts/operations/planning.graphql`, `contracts/fixtures/api/planning-*.json`, `contracts/generated/graphql.ts` (generator only), `server/db/migrations/00019_planting_plans.sql`, `server/db/planning_test.go`, `server/db/generated/` (generator only), `server/internal/planning/period.go`, `server/internal/planning/period_test.go`, `docs/features/planting-planner.md`, `docs/plan/tasks/PLAN01.md`, `docs/plan/tasks/PLAN01-contract-change.md`, `.artifacts/agents/PLAN01/`. Integrator owns graph/ledger.

Acceptance: Freeze additive planning contracts and a four-table migration for independent plans/actions/guidance snapshots/one garden link. Preserve period precision, nullable unknowns, exact budget values, parent workspace scoping, ownership separate from completion, aggregate revisions and deletion behavior. Include validated pure Go period values and meaningful PostgreSQL migration/constraint tests. Generate clients/models reproducibly. Keep planning SDL staged out of the server until real resolvers land in PLAN02; no public stubs, UI links, runtime updates or production data changes.

Verify: Contract validation/codegen and TypeScript check; pure value tests; disposable PostgreSQL 17 up/down/reapply and cross-workspace/period/budget/ownership/provenance tests; existing DB regression tests; generation no-diff, Go build/vet and plan/boundary gates. Do not start legacy or production database containers for tests.
