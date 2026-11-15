# PLAN05 — Page read composition

Add `plantingBoard(input, first, after, filter)`, a read-only workspace-scoped
connection returning existing plan fields and each plan's first 25 projected
windows. Reuse planning list pagination and PLAN04's pure projection service;
do not create competing scheduling rules in Angular. Its page cursor wraps the
existing workspace/filter-bound cursor and binds the requested month view and
assumptions. Plan rows without dates or supported guidance remain present.

The response includes the workspace timezone/local day for an honest default
horizon and date labels. Each nested schedule retains its own pagination cursor;
the existing per-plan query loads additional windows on detail. At most 100 plan
rows per request; source previews are limited to 25 windows per row and charged
at their real maximum. No scraping, model call or automatic write occurs on page
load. Existing per-plan query and write semantics remain unchanged.

This additive query supplies the UI's collection composition in one HTTP call.
Omitted projection assumptions reuse that plan's saved selection assumptions
only while the accepted research context still matches current configuration.
An explicit assumptions object overrides that default, including an empty one.
Dates never recur automatically into another year or leak into another plan.
Calendar/Year/Today projections and reminders remain PLAN06. Garden promotion
and planning assistant tools remain PLAN07/PLAN08; no inactive product controls
are added for those unfinished flows.
