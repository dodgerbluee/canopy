# PLAN03 — Crop-based regional planning guidance

Status: IN_PROGRESS. Local checks passed; GitHub CI pending.

Implemented crop/region research for future plans without creating owned plants.
The existing knowledge service supplies reviewed regional publications, fetching,
configured GARDEN connection and OpenAI-compatible/Anthropic/Gemini transports.
The registry now has eight crop families and twelve publications, including
Texas/Oregon tomatoes and UK strawberries. Other crop/region pairs remain manual.

Plan saves and relevant edits create a transactional guidance outbox. Explicit
refresh deduplicates unchanged pending work while preserving revision, audit and
idempotency rules. Country/region changes invalidate active generations in the
same transaction. A bounded periodic River sweep uses leases, short parent
transactions and generation/context/lease fencing. Late work cannot overwrite
new intent, cancellation or accepted source history. Provider waits hold no plan
row lock; session locks are released or their connection is closed on failure.

Research receives public crop/source material only. Private plan names, notes,
location labels and quantities are locally filtered or hashed, never sent to the
model. Public text/extraction caches are separate from workspace snapshots and
reuse across target years and workspaces. Failed source attempts count toward
the atomic eight-calls/workspace/UTC-hour limit; cache hits are free. Calls use
the existing 5,000-token limit. Rebuildable caches/counters are excluded from
operator backups and user exports; snapshot evidence is retained.

Source quotes, phase-specific complete clauses and applicability are validated.
Typography/whitespace normalization preserves words and numbers. Unsupported
claims are discarded; deterministic explicit-clause fallback uses the same
validators and records its own producer identity. Reviewed multi-crop sections
exclude neighboring crops and fail closed on changed boundaries. Incomplete
phase windows or ambiguous mixed strawberry types remain unplottable. Annual
day/month/season, multiple/cross-year windows and frost-relative offsets retain
precision; no guessed frost date, dose or first-year fruit. Selected dates,
actions and exact budgets are never rewritten by enrichment.

Executed checks and logs:

- Focused real PostgreSQL domain/provider/HTTP/portability/schema tests passed
  (`focused-tests.log`). Includes automatic outbox, pending dedup, accepted
  history, stale callbacks, cancellation/configuration races, expired leases,
  invalid provider results, pool contention and bounded sweeps.
- Provider tests cover public cache reuse, workspace isolation, private prompt
  exclusion, atomic concurrent quota reservations/failures, existing owned-plant
  extraction, and an actual queued River job reaching completed with READY data.
  Saved-connection protocol mocks cover all three supported transports.
- Current populated, pre-planning and populated pre-migration-20 restore passed.
  Missing operational scheduling metadata stays null; READY snapshot content and
  timestamps stay immutable while its internal next-check value can advance.
- Migration CLI applied all migrations, then executed 20 -> 19 -> 20 in a second
  empty disposable database (`goose-up-down.log`).
- Node24 contract validation and actual GraphQL response completion passed for
  all four planning fixtures, including pending refresh with accepted history
  (`contracts-fixtures.log`).
- Recorded primary-source/model cases passed the source adapter/validation
  pipeline (`source-replay.log`). A fresh three-source run using the configured
  local Strata model also passed, with actual planting facts and usable timing
  for each publication (`live-smoke.log`, 58.61 seconds).
- Whole Go regression passed with real PostgreSQL (`go-regression.log`). The
  final domain/provider/HTTP paths passed the race detector (`race-tests.log`),
  including conservative missing-section handling and lock cleanup changes.
- Lint passed Go format/vet, Node24 contract TypeScript and Angular production
  build (`lint.log`). Generation no-diff, dispatch, plan/packet and import-boundary
  gates passed (`generation-no-diff.log`, `final-plan-boundary.log`). GitHub CI
  conclusion pending.

Only the disposable canopy-plan03-pg database is used. Live extraction sends
public publications to the configured local Strata model; no production
database, connection settings, garden records, app runtime or deployment is
changed. No UI, schedule projection, reminder, garden promotion or MCP tool is
included in this packet. Raw logs and recorded public drafts remain ignored.

Next: PLAN04, the pure schedule engine and explicit selection/projections.
