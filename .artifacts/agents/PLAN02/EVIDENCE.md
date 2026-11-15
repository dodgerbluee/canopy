# PLAN02 — Planning domain, CRUD API and portability

Status: ACCEPTED. Implementation commit: e67c34f01cc8a50180d812c2ee64a42b1de9f0fb.

Full GitHub CI passed on that commit:
https://github.com/dodgerbluee/canopy/actions/runs/43677855402

Implemented two planning queries and eight mutations using the accepted PLAN01
contract. Domain commands are independent of GraphQL/provider code, use injected
clock/IDs and generated scoped SQL, and commit plan/action changes, aggregate
revisions, audit rows and idempotency receipts together. Expired keys can be
reused; matching concurrent retries create one result. Failed commands roll back.

A plan can remain an unknown-date idea, and exact budgets remain estimates.
Chosen periods preserve year/season/month/day precision. Cancellation remembers
progress, preserves open actions and invalidates their reminder revision. Current
guidance never substitutes an old generation; accepted provenance remains visible
until the user changes their chosen period. No actual garden/care/spending records
are created as side effects.

The server now registers planning SDL with real resolvers. All ten frozen client
operations execute over HTTP. Workspace/member/authentication behavior, nullable
fields, scalar dates, conflict problems and safe integer narrowing are verified.
Planning list cursors bind workspace/filter/order; literal search and duplicate
name pagination are tested. Query-cost checks charge bounded nested lists.

All four tables are included in scoped user exports and operator backups. A
populated backup/restore preserves complete rows, accepted-source cycles, exact
selected months, open future actions and nullable deleted-plant markers. A valid
legacy archive without planning tables still restores its existing garden.

Executed focused checks against disposable PostgreSQL 17:

- Pure intent/action/state validation and precision tests.
- Create/replace/isolation, exact/unknown/free budgets, full null replacement,
  concurrent replay and edit conflicts, receipt conflict/gone/expiry, audit-failure
  rollback, child scope/state/limit checks, reminder schedule revisions,
  accepted-generation history and injected completion time.
- Actual ten-operation GraphQL client journey; bad scalar/input/page values,
  integer wraparound rejection, private/absent ID parity, unauthenticated read,
  member writes, typed annual cross-year guidance and source retrieval dates.
- Populated backup/restore row comparison and manifest reconciliation, scoped
  export with citations/links and legacy archive compatibility.
- Complete Go regression suite passed with real PostgreSQL, including existing
  collection, care, identity, insights, media, GraphQL, worker-related and
  portability tests. New domain/HTTP checks also passed with the race detector.
- Generation no-diff, lint (Go vet/format, Node24 TypeScript and Angular build), frozen
  operation limits, task dispatch and plan/boundary gates passed locally.
- GitHub CI passed release tooling, clean dependency installation, generation
  no-diff, plan/boundary gates, lint/build, unit tests and real PostgreSQL
  integration on the final implementation commit.

No production database, provider configuration, app container or deployment was
changed. The named disposable canopy-plan02-pg test instance was removed after
local verification. Changes are pushed to feature/planting-planner, without a
main-branch merge or deployment image release. There is
no new UI, source ingestion, reminder job, garden promotion or agent tool yet.
Raw logs remain ignored under this directory.

Next: PLAN03, crop-based source guidance and enrichment for future plants.
