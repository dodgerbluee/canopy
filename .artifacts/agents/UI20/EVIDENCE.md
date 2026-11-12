# Public acceptance record: UI20

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| 2026-11-04 | UI20 | ACCEPTED | Calendar photos now use capture dates, with unknown/upload-fallback dates excluded; Photos taken labels. Real PostgreSQL GraphQL regressions, 28 units, build, nine browser scenarios/zero axe; paired runtime refresh, queued worker proof and publication. Evidence: `.artifacts/agents/UI20/`; packet: `docs/plan/tasks/UI20.md`. |

# UI20 — Calendar photo capture dates

Contract change request: accepted user correction. Supersedes UI19 date-added semantics: calendar photo events use `taken_at` for USER/EXIF dates, bucketed in workspace timezone. Null/UPLOAD_FALLBACK capture dates are excluded rather than invented. No stored photo records or other calendar events change. UI labels become Photos taken/Photo taken.

Status: ACCEPTED.

Executed evidence in `.artifacts/agents/UI20/`: `backend.log` real GraphQL rain and capture-date regressions including old captures uploaded later, DST, unknown/upload-fallback dates and deletion; `web-checks.log` 28 units and production build; `browser-report.json` nine desktop/phone/theme/filter/rain/error scenarios, explicit Photo taken label and zero axe violations; `generation.log` generated bindings; `worker-proof.log` actual new worker consumed queued snapshot through real Docker OCR; `runtime-live.json` paired API/worker refresh preserving database/media/provider settings; `published.json` and `live-check.json` new build on local ports 4300/4311 and phone port 4313. No owner photo records changed.

