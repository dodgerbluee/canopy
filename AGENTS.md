# AGENTS.md — working rules for this repository

## Scope and dispatch discipline
- Work only on a task whose dependencies are all ACCEPTED in
  `docs/plan/task-graph.json` (`make dispatch-check TASK=Txx` enforces this).
- Touch only paths your task packet's `Own:` list claims (`docs/plan/07-tasks.md`).
  Parent groups (T01/T02/T32/T38) are not tasks; dispatch their children.
- Contracts in `contracts/graphql/`, `contracts/operations/`,
  `contracts/fixtures/api/` are authoritative once accepted. Implementers may
  not invent competing semantics; raise a change request instead.

## Evidence discipline
- Every acceptance claim must come from an executed command. Never report an
  unexecuted check as passing.
- Write evidence to `.artifacts/agents/<TASK>/` and add a ledger row in
  `docs/plan/ledger.md`. ACCEPTED without evidence is a validator failure.

## Toolchain (ADR-0001)
- Node 24 via the pinned `node:24` container for ALL web/contract tooling:
  `docker run --rm -v "$PWD":/w -w /w node:24 …`. Host Node is unsupported.
  No global installs.
- Go 1.26 host toolchain; sqlc runs via `go tool sqlc` (tool pin in
  `server/go.mod`). Postgres 17 dev/test via `make db-up` (disposable).
- Generated code (`server/db/generated/`, `contracts/generated/`) is
  committed and reproducible: change queries/SDL/operations, then `make gen`.
  Never hand-edit generated files.

## Import boundaries (`scripts/boundary_check.py`)
- `canopy/server/db/generated` may only be imported from `server/db/**` or
  `server/internal/**`; `server/cmd/**` goes through internal packages.
- `web/src/app/ui/**` (presentation primitives) may not import
  `@canopy/contracts` or the data layer; data access lives in `web/src/app/data/**`.

## Secrets and safety
- Provider/model metadata can contain nested credentials. Print only explicitly
  allowlisted public fields; never dump complete provider/model objects.
- No secrets in code, logs, fixtures, or evidence. Dev credentials
  (`postgres/devpw`) are disposable container defaults, not secrets.
- Experiments stay disposable: spike workspaces under /tmp, test schemas are
  dropped after use.

## Commands
- `make check` — full gate: plan/packet/dispatch validation, boundary check,
  generation no-diff, lint, unit tests, integration tests.
- Suite selectors for not-yet-built suites (`test-e2e`, `test-visual`,
  `test-auth`, …) fail loudly with PENDING. They are never success stubs.

## Refreshing the local runtime
- API and background worker must run compatible builds from the same checkout.
  When updating shared server code (especially analysis snapshots), rebuild
  and restart both before reporting the local stack updated. Frontend-only
  updates do not require restarting either backend process.
- Preserve the configured database, provider settings and shared
  `CANOPY_STORAGE_ROOT` when restarting. Verify an actual queued job reaches
  the worker; API readiness alone does not prove background processing works.
