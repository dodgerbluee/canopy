# PLAN04 — Pure schedule engine and explicit confirmation

Status: IN_PROGRESS. Local checks passed; GitHub CI pending.

Implemented a pure projection engine that receives immutable source facts,
explicit planning assumptions, selected period, civil month range and local
today. It never reads a clock, database or provider. Annual day/month/season and
absolute precision remain intact. Separate clipped month bands are rendering
bounds, not concrete planting days. Prior-year starts overlap correctly; distinct
windows remain separate. Unknown anchors/offsets, hemisphere conflicts, invalid
leap dates and year overflow produce explicit review states. Relative estimates
use only the named anchor and civil-day arithmetic. Typical seasonal harvest
does not assert a newly acquired plant will produce in its first year.

Added the scoped paginated plantingSchedule query and confirmPlantingSchedule
command, with real typed HTTP operations. Reads use workspace-local today,
explicit site/garden latitude and optional planning assumptions. They never
fetch sources, enqueue work or call the model. Cursors bind plan/revision,
source/context, view, assumptions and local day; background next-check metadata
does not invalidate them. Manual periods remain useful with unavailable/stale AI.

Confirmation requires active state, expected revision, current READY source
generation/context, verified extraction provenance, eligible method/phase and a
chosen period contained by its resolved source window. Exact chosen days are
explicit user input. Target, accepted pointer, selection metadata, aggregate
revision, audit and replay receipt commit together. Actions, lifecycle, budget,
research generation and actual garden activity do not change. Refresh preserves
choices; manual target edits detach acceptance. Historical accepted seasons keep
their original hemisphere even after site changes.

Migration 21 adds one nullable bounded JSON column, preserving old populated
archives. Export/backup include versioned selection inputs, source precision,
effective hemisphere and confirmation time through existing planning tables.

Executed checks:

- Pure precision, distinct/cross-year overlap, seasons in both hemispheres,
  signed/unknown relative intervals, leap day, year overflow, method/harvest,
  containment, immutability and deterministic output checks.
- PostgreSQL/HTTP scoped projections, pending/disabled/manual paths, chosen-day
  confirmation and exact source precision; source/current-context refusal,
  conflicts, replay, audit rollback and twelve competing confirmations through a
  two-connection pool. No real plants/purchases/activities are created.
- Existing and new populated/legacy backup/restore/export tests preserve current
  assumptions and old accepted pointers without synthesizing missing metadata.
- Actual migration CLI applied all migrations and executed 21 -> 20 -> 21 in a
  second empty disposable database (`goose-up-down.log`).
- Typed fixture and contract checks passed using Node24 (`contracts-fixtures.log`).
- New pure/domain/HTTP paths passed Go's race detector (`race-tests.log`).
- The complete 400-year Gregorian leap cycle produced 97 exact February 29
  dates and 303 review states, without clamping to another day.
- Whole Go regression passed with real PostgreSQL (`go-regression.log`), including
  existing collection, care, identity, knowledge, insights, media and portability.
- Lint passed Go format/vet, Node24 TypeScript and Angular production build
  (`lint.log`). Generation no-diff and plan/packet/dispatch/import-boundary gates
  passed (`generation-no-diff.log`, `final-plan-boundary.log`). Final source-linked
  portability fixtures passed (`final-portability.log`). GitHub CI pending.

Tests use the disposable canopy-plan04-pg database, separate from running app
databases. No production configuration, records or runtime is changed by PLAN04.
No new UI, Calendar/Year/Today integration, reminders, garden promotion or MCP
tool is included. Raw logs remain ignored in this directory.

Next: PLAN05, the Angular Planning page and shared modal flows, authorized to
start after PLAN04 acceptance by the user's latest instruction.
