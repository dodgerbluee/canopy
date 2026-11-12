# Public acceptance record: UI21

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| 2026-11-05 | UI21 | ACCEPTED | Regional plant knowledge, explicit location opt-in and rounded seasonal year tracks with sourced garden Q&A. Durable background enrichment, cached regional adapters, truthful timing/unknowns, isolated stale-safe writes and export/restore. 441 frontend units, final focused checks, affected Go/PG suites, nine browser scenarios/zero axe, real global geocoding, all nine guide/model adapters (one targeted retry), exact queued worker + real-model answer, generation/contracts/vet/plan gates and local/phone publication. Evidence: `.artifacts/agents/UI21/`; packet: `docs/plan/tasks/UI21.md`. |

# UI21 — Regional knowledge and seasonal year tracks

Status: ACCEPTED.

Additive contract change request: accepted user request for background regional plant knowledge, rounded seasonal tracks and source-backed garden questions. `contracts/graphql/knowledge.graphql` and its generated operations define this advisory surface. Existing reviewed care profiles, activity commands, manual watering and care completion semantics are unchanged. AI extraction never marks a profile human-reviewed.

## Shipped behavior

- `/calendar/year`: one photographed plant row with rounded seasonal bands, overlapping lanes, shared Month/Year navigation, searchable plants, type toggles, year selection, actual recorded counts, source/condition dialogs and garden questions. Sticky plant identities, keyboard scrolling and scoped asynchronous modal focus work on phones. Unknown timing does not generate a band.
- Explicit country, region, place and timezone in a consistent modal, plus city/postal search through Open-Meteo/GeoNames, optional device coordinates rounded to three decimals, and manual hardiness zone/system. No IP lookup or timezone-to-climate inference. Lookup/enrichment consent is explicit and defaults off. Settings are also available from Settings.
- Plant creation/import and identity/status edits atomically insert/update a durable enrichment intent. A River worker sweeps every 30 seconds, with bounded claims, lease recovery, one active sweep per schema, document/profile locks and generation-fenced commits. Saving geography refreshes existing plants; transient failures retain the plant and expose retry.
- Nine registered primary-source guides: Texas citrus, fig, blackberry, pomegranate, strawberry, avocado and olive; California citrus; Pacific Northwest strawberry. Country/region/crop/hemisphere are explicit adapter metadata. Unrecognized botanical identities, plant types or regions remain unsupported. No relocation of California advice to Texas.
- Registered HTTPS sources only, robots/access checks, redirect allowlist, non-public-address blocking, bounded downloads/text/model replies, resource-limited PDF decoder path, reusable 30-day public document and workspace profile caches. Retained content consists of bounded factual paraphrases and provenance, not publication mirrors.
- Seasonal timing is parsed deterministically from source evidence. Timing must occur within the same supporting passage. Continuous versus discrete months, winter wrap, season qualifiers, negative/relative language and explicit hemispheres are handled. Unsupported statements are omitted; unverifiable timing remains untimed.
- Garden answers use plant identities, source facts and recorded yearly activity/photo counts. Source/plant references are validated, missing knowledge produces a deterministic unknown-timing/counts response, and answers never write care records. One question per HTTP request, 12 attempts/workspace/hour/process, bounded model context and response, cancellation and a targeted long-answer HTTP deadline.
- Knowledge can use a dedicated OpenAI-compatible chat endpoint via `CANOPY_KNOWLEDGE_*`; otherwise it reuses the configured vision chat endpoint. The legacy `/v1/analyze` text adapter is not treated as chat-compatible. Credentials stay in runtime configuration.
- Settings and profiles participate in export and backup/restore. Public source caches and processing intents are regenerable; restored plant rows recreate intents. Owner plant/photo/purchase records were not edited by tests.

## Limits stated in the product/docs

Hardiness zone is entered manually; no automatic hardiness-map lookup is implemented. Source coverage is intentionally explicit and currently limited to the registered crops/regions. Seasonal bands are typical source guidance, not confirmed bloom events, health diagnoses, guaranteed harvests, exact fertilizer doses or weather-adjusted forecasts. Garden questions use recorded counts rather than photo analysis or purchase totals. Guidance does not automatically become a reviewed care plan.

## Executed evidence

All evidence is in `.artifacts/agents/UI21/`:

- `backend-regressions.log`: real PostgreSQL integration suites for knowledge/GraphQL/portability, including export/restore. `final-backend.log`: final taxonomy, source/timing, generation fencing, ownership, empty-knowledge and reference-validation regressions. `config-build-tests.log`: endpoint configuration and composition-root checks; `go-vet.log`: Go vet.
- `live-sources-model.log`: nine readable permitted guides and eight successful extraction subtests; the initial blackberry model request failed. `blackberry-live.log`: targeted retry passed. `live-adapter-summary.json` records the combined evidence honestly, including that retry.
- `geocoding-live.log`: actual global location lookup, with explicit country/region/coordinates/timezone.
- `queued-worker.log`: real HTTP plant creation → durable outbox → actual queued current worker → A&M guide → configured local model → two READY plants sharing one profile, followed by a real-model answer with validated supporting references. All records/storage used disposable schemas/directories.
- `web-full.log`: 441 frontend unit tests passed. `web-final.log`: 32 focused tests after shared-navigation cleanup. `web-build.log`: final production build without budget warnings.
- `browser-report.json`: nine desktop/phone/theme/small-screen/error/disabled/empty/HTTP-phone scenarios; band overlap, navigation, sources, retry identities, chat and location forms; zero axe violations and no document overflow. Screenshots use clearly synthetic API fixtures, not owner records.
- `contracts.log`, `generation-proof.txt`: all documents/types validated, generated bindings byte-identical on regeneration. `plan-gates.log`: DAG/packet/boundary checks.
- `migration-applied.log`: migration 00011 applied locally. `runtime-live.json`, `runtime-binaries.json`: compatible API/worker builds with preserved database, shared media and provider settings. `published.json`, `live-check.json`: same frontend build on ports 4300, 4311 and LAN 4313.

Published: `http://localhost:4300/calendar/year`, `http://localhost:4311/calendar/year`, `http://192.168.20.159:4313/calendar/year`. Start with Garden location and explicitly enable regional guidance; this queues existing plants as well as future additions.
