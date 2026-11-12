# Public acceptance record: UI09

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| UI09 | 2026-11-03 | Receipt-first purchase modal: quiet initial validation, keyboard-accessible upload before create, explicit extraction/review, every readable receipt line with exact money and preserved typed fields; staged receipt attachment with stable retry keys and saved line/allocation IDs after partial failure; local Strata runtime, root tests/review/integration | `.artifacts/agents/UI09/` (SUMMARY.md, unit-all.log, build.log, browser-results.json, screenshots, integrated-hashes.json, live-check.json) | Node24 frontend 359/359 in 39 files; production build passed; Chromium phone/desktop × light/dark receipt-first/save/attach-failure/retry/dirty-guard journeys passed, axe0 and no page/console errors; exact verified build served on localhost:4300 + Docker4311, existing API healthy |

# UI09

Receipt-first purchase entry, scoped by packet. Runtime implementation through sequential local Strata runs; root owns tests, review and integration. Frozen contracts; no backend/provider changes or commits.
