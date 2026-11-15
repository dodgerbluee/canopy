# PLAN02 — Planning API

Status: ACCEPTED. Evidence: `.artifacts/agents/PLAN02/EVIDENCE.md`.

### PLAN02 — Planning domain, CRUD API and portability

Depends: PLAN01.

Own: `server/internal/planning/`, `server/db/queries/planning.sql`, `server/db/queries/portability.sql`, `server/db/generated/` (generator only), `server/gqlgen.yml`, `server/internal/transport/graphql/resolver.go`, `server/internal/transport/graphql/planning_resolvers.go`, `server/internal/transport/graphql/planning_guidance.go`, `server/internal/transport/graphql/planning_http_test.go`, `server/internal/transport/graphql/limits.go`, `server/internal/transport/graphql/limits_test.go`, `server/internal/transport/graphql/gql/` (generator only), `server/internal/portability/backup.go`, `server/internal/portability/export_worker.go`, `server/internal/portability/planning_test.go`, `contracts/graphql/planning.graphql` (activation comments only), `docs/features/planting-planner.md`, `docs/plan/tasks/PLAN02.md`, `.artifacts/agents/PLAN02/`. Integrator owns graph/ledger.

Acceptance: Implement the accepted PLAN01 contracts with real workspace-scoped plan/action CRUD, legal lifecycle transitions, aggregate revisions, exact optional budgets and precision-preserving periods. Use transactional audit and replay-safe mutation receipts; never create real garden/care/spending records as side effects. Bind keyset cursors to workspace/filter/order. Register live GraphQL only with working resolvers, bounded reads and member permissions. Include all four planning tables in scoped exports and populated backup/restore, preserving the deferred accepted-guidance cycle and purge markers. No UI, AI ingestion, MCP registration or production/runtime changes.

Verify: Pure validation/lifecycle/pagination tests; real PostgreSQL isolation, concurrency, idempotency, rollback, audit, null replacement, generation/provenance and action limits; actual HTTP operations including malformed inputs and authentication; populated export/restore round-trip and old empty archive compatibility; existing regressions, generation no-diff, lint/build and full CI.

Implementation notes for the next packet:

- The live API has two queries and eight real mutations; no navigation is added.
  Mutation receipts retain minimal plan references, and replay hydrates current
  committed state using the existing collection protocol. Expired keys can be
  reused even before the cleanup job removes their old receipts.
- Intent edits invalidate guidance when identity, category, quantity, method,
  intended place/placement, notes or chosen period changes. Budget-only edits do
  not invalidate planting context. A period change detaches acceptance, retaining
  its source snapshot. No enrichment job is queued until PLAN03 implements it.
- Planning guidance read bounds are shared domain constants: 32 facts/snapshot,
  8 windows/fact, 16 conditions/fact and 16 snapshot assumptions. PLAN03 must
  enforce these before READY, plus actual source/citation/context validation.
  The current adapter validates structural output without claiming verification
  of a synthetic or manually inserted source. Corrupt output fails explicitly.
- Actions remain OPEN when a plan is cancelled. Cancel/restore advances their
  schedule revisions so PLAN06 can invalidate reminder delivery. No notification
  or actual care event is created in this packet.
- Backup uses its existing repeatable transaction and restore uses its existing
  single transaction. The accepted-guidance FK is initially deferred, so loading
  plans before snapshots preserves the cycle without rewriting immutable facts.
- Planning page data loads plan rows and their three child types in four queries
  per page, with a repeatable snapshot. Location/linked-plant output reuses the
  existing scoped collection adapters. Agent catalog remains 84 tools; new
  planner tools and permissions belong to PLAN08.
