# Public acceptance record: UI10

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| UI10 | 2026-11-04 | Fixed stale receipt recovery: old worker rejected current API snapshot before OCR; rebuilt/restarted worker with canonical shared storage, explicit terminal retries get fresh forced attempt, uncertain retries preserve exact body/key, errors preserve draft honestly; local Strata runtime, root tests/ops/review/integration; documented paired API/worker runtime refresh | `.artifacts/agents/UI10/` (SUMMARY.md, worker-before.log, worker-after.log, go-checks.log, unit-all.log, build.log, browser-results.json, live-check.json) | Exact old-worker STALE/attempts0 reproduced; current real queued Docker OCR SUCCEEDED/attempts1; Go8 checks passed; frontend366/366; four browser theme/viewport journeys including stale→fresh read passed, axe0/errors0; both local UIs published and worker alive |

# UI10

Investigate receipt run becoming STALE before OCR; synchronize local worker and API code without touching owner terminal runs. Implement receipt retry through sequential local Strata only; root owns tests, operational restart, review and integration. Frozen contracts.
