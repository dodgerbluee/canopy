# Public acceptance record: UI19

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| 2026-11-04 | UI19 | ACCEPTED | Garden calendar with plant/type visibility filters and manual area rain; additive typed API, photo-added/workspace-day read model, exact optional rain depth, idempotent create and guarded removal, export/restore. 430 frontend units; affected Go PostgreSQL suites; 9 browser scenarios/zero axe; production build, contracts/generation/vet, queued worker proof and live local/phone publication. Evidence: `.artifacts/agents/UI19/`; packet: `docs/plan/tasks/UI19.md`. |

# UI19 — Garden calendar and manual rain

Status: ACCEPTED.

Calendar is a guarded shell route at `/calendar`, in desktop and mobile primary navigation. Desktop plant list includes archived plants for historical filtering, name search, all/clear and per-plant checkboxes; mobile has the same visibility controls in the shared modal. Independent watering/photo/other/area-rain toggles, month controls, per-day icon counts and a selected-day detail list. Uses existing cover photos with a leaf fallback. Filters never change plant records.

The additive contract is documented in [UI19-contract-change.md](UI19-contract-change.md). Calendar reads canonical journal sources in complete cursor pages. Photos use `plant_photos.created_at`, other journal entries use recorded time in workspace timezone. Explicit rain dates stay civil dates.

Add rain uses the shared entry dialog, selected date, optional exact inches/mm depth and note. Unknown depth stays null. Stable request identity permits safe retries; new form sessions get new identities. Rain removal is confirmed and revision guarded. Rain is workspace area context: it creates no WATER activity or care completion. No automatic weather prompts were added.

Rain records are included in user exports and backup/restore. Migration 00010 applied locally; matching API/worker builds preserve the existing database, shared media storage and managed provider configuration. Owner records were not used for mutation tests.

Executed evidence in `.artifacts/agents/UI19/`:

- `calendar-backend.log`: real HTTP/GraphQL lifecycle, idempotency, audit, isolation, revision conflict, removal, range/cursor validation, photo-added dates and workspace DST days.
- `backend-regressions.log`: all affected journal, GraphQL and portability packages on disposable PostgreSQL schemas; explicit rain export and backup/restore assertions.
- `web-full.log`: 430 frontend unit tests passed. `calendar-units.log`: focused calendar/navigation state and date/depth/pagination tests.
- `web-build.log`: production build, no budget warnings. `contracts.log`, `generation-proof.txt`, `go-vet.log`: document/type validation, byte-identical generation and Go vet.
- `browser-report.json`: nine browser scenarios, desktop/phone light/dark, 320px, HTTP phone mode, failure/empty/pagination cases; safe rain retries, separate new rain intents, removal, filtering and month navigation; zero axe violations. Screenshots are API fixtures, not owner records.
- `worker-proof.log`: actual newly built worker executable consumed a queued receipt snapshot and completed real Docker OCR in a disposable schema.
- `migration-applied.log`, `runtime-live.json`, `live-check.json`, `published.json`: local migration, paired runtime refresh, identical Calendar build and authenticated API proxies on ports 4300/4311 and LAN 4313.

Published: `http://localhost:4300/calendar`, `http://localhost:4311/calendar`, `http://192.168.20.159:4313/calendar`.
