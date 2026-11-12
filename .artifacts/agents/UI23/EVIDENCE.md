# Public acceptance record: UI23

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| 2026-11-06 | UI23 | ACCEPTED | Preserve source block boundaries and complete timing, audit omitted harvest windows, repair only invalid/missing facts without duplicates, version and reprocess old caches. Go/real PostgreSQL regressions; three live A&M/model harvest checks; exact queued-worker proof; all supported garden plants refreshed and owner record hashes unchanged. Evidence: `.artifacts/agents/UI23/`. |

# UI23 — Complete source-backed timing ingestion

Status: ACCEPTED.

## Implemented behavior

- HTML paragraph, heading, list and table boundaries are retained through extraction and validation. Source context cannot merge separate climate, harvest, irrigation and soil rows. PDF line wraps remain whitespace while paragraph breaks are preserved.
- Calendar timing must account for every dated clause in the full supporting source sentence and the summary. Partial quotes cannot hide an October continuation or a second crop. Different actions cannot widen each other's calendar bands. Separate explicit dates remain usable alongside relative formulas in other sentences.
- An independent bounded audit finds explicit positive dated harvest statements in the guide, catching an entirely omitted crop window in a separate sentence. Missing coverage triggers repair. Remaining coverage gaps keep harvest guidance untimed rather than presenting a partial calendar as complete. This audits recognizable explicit statements; it is not a general proof that every horticultural claim is correct.
- One targeted repair attempt corrects invalid facts and missing harvest passages while preserving already validated facts. Extra repair facts are accepted only when they fill genuinely missing harvest concepts, preventing duplicate replacement notes. Unsupported facts are omitted. Uncertain timing remains an untimed note; the model cannot set the private fallback flag.
- The parser preserves discrete months, compound seasons, range endpoints and year wrap. Mixed month/season wording is approximate. Hemisphere mapping applies only to seasons, keeping explicit calendar months fixed. Negative/relative timing stays off the calendar.
- Facts retain extraction version 4 and bounded exact timing text for provenance. Profile identity includes the version. Old-version documents/profiles are bypassed, stale active opted-in plants are automatically requeued by the worker, and reads hide old-version guidance until refreshed. Disabled/unsupported plants and generation fences retain their existing behavior. No GraphQL/schema migration or frontend changes were required.

## Executed evidence

Evidence under `.artifacts/agents/UI23/`:

- `regressions.log`: knowledge unit/regression suite and real PostgreSQL GraphQL knowledge tests, including fresh-cache invalidation, automatic requeue, stale-read suppression, profile reuse, consent, ownership and generation fencing.
- `harvest-live.log`: fresh public A&M fig, pomegranate and strawberry guides through the configured model; complete fig windows (May, August–November, approximate) pomegranate September–October and strawberry March–April verified. Includes allowlisted per-fact validation diagnostics without provider credentials.
- `queued-worker.log`: actual final queued worker/source/model integration proof.
- `generation-proof.json`: generated SQL bindings byte-identical on regeneration; `vet.log`: affected packages vetted.
- `regressions-database-recovery.log`: broader integration run interrupted by local disk exhaustion/PostgreSQL recovery; final affected tests were rerun after space recovery. Portability suite passed in that run. `cache-cleanup.json`: only old regenerable Go cache entries removed to restore 2 GiB free.
- `runtime-live.json`, `runtime-refresh.log`: matching API/worker restarted with preserved database, media storage and managed model connection.
- `owner-records-before.json`, `owner-records-after.json`: counts and row hashes prove plants, photos, activities, journal and purchase data unchanged.
- `garden-live.json`: all ten supported active plants READY with extraction version 4; pomegranates September–October, figs May plus August–November (approximate seasonal mapping), strawberries March–April. Existing unsupported plant remains explicit. All three local year URLs return 200; API ready and matching processes alive.
- `portability.log`: export/restore tests passed. `plan-gates.log`: plan, packet, dispatch and boundary checks.

Published through the existing local instances: http://localhost:4311/calendar/year and http://192.168.20.159:4313/calendar/year. No frontend rebuild was needed. Seasonal windows remain typical source guidance, not ripening deadlines or observations.
