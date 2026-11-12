# Public acceptance record: UI04

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| UI04 | 2026-11-03 | Local content-aware canopy framing for plant cards and heroes; central foliage band detection suppresses edge greenery/grass and recomputes CSS crop from real container geometry. Full composition in journal/gallery. Original photos, capture dates and chosen cover identities retained | `.artifacts/agents/UI04/` (SUMMARY.md, full-unit.log, build.log, browser-after.json, before/after screenshots, integrated-hashes.json) | 334 frontend unit tests; Node24 production build; actual ten imported cover files in browser fixtures at 390/1440, grid/list/hero + full journal/gallery, no page errors or overflow; resize updates crop with unchanged pixel-sample count; baseline-checked integration; both localhost4300/4311 serve exact verified build; API healthy. No new provider/contracts/dependencies. |

# UI04

Owner reports cover photos show trunks without foliage. UiPhoto always uses centered object-fit cover. Fix with a pure bounded foliage-focus detector and container-aware crop position; explicit auto framing for plant cards/heroes, full view for documentary gallery and journal. Browser only, existing authenticated image load, no new API calls/AI provider use, no original image changes. Runtime implementation through sequential local Strata model in isolated UI04 source copy; root owns tests/format/build/visual review/integration. No commits, credentials, unrelated paths, services or source changes.
