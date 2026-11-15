# PLAN04 — Projection and confirmation contract

Additive implementation of the fourth user-authorized planting planner step.

- Add `plantingSchedule(planId, input, first, after)`, a workspace-scoped nullable
  connection. Input is an inclusive canonical month range of one to twelve
  months, optional explicit hemisphere and at most six distinct anchor kinds.
  Anchors are explicit planning assumptions, never recorded garden events.
  Pagination binds workspace, plan/revision, source/context, range and assumptions.
  The local day is also bound; operational next-check updates are not.
  No-date/unknown windows remain visible for review; resolved off-screen windows
  are excluded by overlap, including occurrences starting before the view.
- Projection is pure: civil dates/months, immutable source facts, explicit
  hemisphere/anchors and supplied local today. Source seasons and months retain
  their precision. Separate month bands are approximate display bounds, never
  chosen dates. Missing anchors/offsets, hemisphere conflicts, impossible leap
  days and year overflow return explicit unresolved states. No clamping dates,
  guessing frost from timezone/zone or asserting first-year fruit.
- Source annual seasons use their starting year. Northern winter and Southern
  summer may cross New Year. Relative offsets use civil day arithmetic and only
  their named anchor; a sowing date cannot stand in for transplanting. Seasonal
  harvest is general source advice, not a first-harvest forecast. Only a supported
  relative interval from an explicit establishment anchor can estimate harvest.
- Add `confirmPlantingSchedule`: active plan, expected parent revision, current
  snapshot id/generation, fact id, zero-based window index, occurrence starting
  year, explicit projection assumptions and chosen period. Require READY,
  source-validated provenance, matching current research context, an eligible
  phase/method and a resolved source window containing the chosen period. A
  concrete chosen day is user input, not an inferred window boundary. Manual
  choices remain available through existing intent editing without a provider.
- Confirmation changes only target period, accepted pointer/selection metadata
  and aggregate revision, audit and replay receipt in one transaction. It does
  not change lifecycle, source generation, actions, reminders or real plants.
  A subsequent source refresh never rewrites the chosen period. Manual target
  changes clear accepted pointer and selection; other context edits retain the
  historical choice while current guidance is invalidated.
- Migration 21 adds nullable bounded `accepted_schedule` JSON to plans. It stores
  engine version, selected fact/window/year, effective hemisphere, explicit
  anchors, supported source period and confirmation time. Existing backups with
  an accepted pointer but no selection remain valid. Selection implies a scoped
  accepted snapshot and target. Export/backup preserve it without new tables.
  An accepted season keeps its confirmed hemisphere when garden geography
  changes; fresh guidance can be incompatible without moving the old choice.
- Season geography uses explicit intended-site/garden latitude or an explicit
  supplied hemisphere; a supplied value conflicting with known latitude is
  invalid. Source hemisphere can interpret that source's own seasonal advice;
  it never invents location for an otherwise abstract manual season. Today uses
  the workspace timezone. Queries do not enqueue or trigger provider work.
- The connection is per-plan in this packet. Workspace collection pagination
  remains the existing `plantingPlans` operation. Angular page composition and
  Calendar/Year/Today integration remain their later implementation packets.
