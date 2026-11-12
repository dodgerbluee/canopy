# Public acceptance record: UI22

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| 2026-11-05 | UI22 | ACCEPTED | Whole-garden annual overview with monthly care/season groups, unique plant counts, shared filters and source dialogs. Node24 14 tests/build; nine browser scenarios, axe and overflow checks; local publication. Evidence: `.artifacts/agents/UI22/`. |

# UI22 — Whole-garden seasonal overview

Status: ACCEPTED.

## Behavior

The existing year page now starts its timelines with one merged garden-wide annual tube. Each month combines source-backed seasonal windows, colored by the existing type legend, and counts unique plants with care or harvest windows. Selecting a month shows compact type groups, plant names and unique counts. Groups open the common entry dialog with all contributing plant/source notes; selecting a note opens the existing source/conditions dialog. Individual plant tracks remain below.

Plant search and type filters apply to both surfaces. The initial month uses the configured garden timezone. Mobile shows all twelve months in two rows without horizontal document overflow. Unknown timing never becomes a scheduled action; zero windows do not claim that care is unnecessary, and counts do not imply overdue work or completion. No API, contracts, worker, database or owner records were changed.

## Evidence

- `.artifacts/agents/UI22/web-checks.log`: Node24, 14 passing seasonal/overview tests and production build without budget warnings. Covers per-plant/per-kind deduplication, multiple contributing notes, sparse months, winter boundaries, unknowns, filters and month/group/source state transitions.
- `.artifacts/agents/UI22/browser-report.json`: nine desktop/phone/theme/small-screen/error/disabled/empty/HTTP-phone scenarios passed; monthly groups, filters and source handoff verified; zero axe violations and no document overflow. Screenshots use synthetic fixtures.
- `.artifacts/agents/UI22/published.json`, `live-check.json`: the final build published to existing desktop/progress/phone instances and all nine year/month/settings URLs verified.
