# FIX09 — Sign-in identity and garden rain reviews

Status: local implementation and verification complete; exact-source GitHub CI and requested PR merge pending.

## Findings and changes

- Sign-in seeded `owner` directly in the Angular form. It now starts blank and keeps native username autocomplete. Read-only inspection of the original local garden found **zero** accounts or display names called owner; no user account or garden record was removed.
- The original local garden had workspace coordinates, no plant-location coordinates and zero weather snapshots, despite completed `weather.fetch` jobs. That worker only used plant locations, and there was no rain-confirmation surface.
- Migration 24 adds context-scoped advisory checks. A separate hourly `rain.check` job reads user-entered garden coordinates, without requiring plant locations or care profiles. Elapsed UTC rain/showers intervals are apportioned into IANA civil days, including DST and fractional offsets. Future intervals and snow cannot become rain proposals.
- Today has compact confirmation/dismissal controls, optional exact entered depth, manual Calendar access, last-check and missing/stale state explanations. Model estimates never fill or save the user's amount. Neither path logs watering or completes care.
- Workspace locks, immutable response decisions, exact coordinate/timezone fencing, stable receipts and manual-day suppression prevent duplicate proposal confirmation. Existing manual-rain receipt hashes stay compatible across the added optional input. Backups preserve decisions and confirmed rain references.

## Executed verification

- `make dispatch-check TASK=FIX09`: passed.
- `PGTEST_DSN=<isolated PostgreSQL 17 on 56024> go test -p 2 ./...`: passed, including actual backup/restore of confirmed/dismissed rain reviews, planner and MCP regression. Raw output: `go-postgres-regression.log`.
- Final targeted rain suites: passed. `go test -race -p 2 ./internal/journal ./internal/care/weather ./internal/transport/graphql -run 'TestRecentRain|TestGardenRain|TestRainReview|TestManualRain|TestCalendarRain' -count=1`: passed against PG17. Covers provider units/time bounds/nulls/errors, DST/fractional-offset midnight, no locations or care, cache/failure/staleness, concurrent confirmations, unknown amount, manual rain, isolation and a moved location during fetch. Output: `rain-race.log`.
- Angular full regression: **65 files / 525 tests passed** (`frontend-final-tests.log`). Production build and final compact-layout focused **5/5** tests passed (`final-layout-checks.log`). A receipt-polling test initially timed out during simultaneous image compilation; the full suite passed once that build finished, without changing its implementation or timeout.
- Playwright: **20/20 passed**, repeated after final layout and paired runtime refresh. Desktop and Pixel 7, both themes. Real live dashboard reads and blank-login behavior plus explicitly controlled rain responses for save/retry/depth/dismiss interaction states. Axe zero violations and no horizontal overflow. The actual recording/worker paths are verified separately over real GraphQL/PG17. Output: `final-browser-tests.log`; screenshots visually inspected.
- `make gen-check`: passed; authoritative GraphQL, SQL, gqlgen and 102-tool MCP manifest generation left no diff. `go vet ./...`, whole-server gofmt, contract TypeScript check and fixture validator: passed. Plan/packet/dispatch and import-boundary checks (including negative fixtures): passed. `git diff --check`: passed.

## Preserved local preview

The isolated preview at http://127.0.0.1:7082 runs the exact final image with a matching API/worker, migration 24, retained database, media volume and provider configuration. No Docker socket mount. The original local garden on 4311 and remote deployment were not changed.

The actual hourly rain job completed and fetched public weather using the preview's existing opted-in coordinates. Returned estimates: November 15 **4.800 mm**, November 14 **1.500 mm**, November 13 **0.000 mm**. All remain PENDING; no actual rain/watering was fabricated. After the final matched rebuild, explicitly queued **job 2580 (`rain.check`) completed in one attempt**, using the preserved fresh cache. Readiness and actual live dashboard passed.

## Release/merge

The full eight-step planner was already accepted in PLAN01–PLAN08. This packet adds the requested username/rain corrections before merging that feature branch. Main is unprotected; preserve packet history with a merge commit after exact-source CI succeeds. Main automatically starts the existing dev-image publishing workflow; no stable version tag or production redeployment is implied.
