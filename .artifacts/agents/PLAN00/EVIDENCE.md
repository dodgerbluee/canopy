# PLAN00 — Future planting planner design

Status: ACCEPTED. Deliverable: docs/features/planting-planner.md.

Reviewed actual routes/navigation, collection and acquisition inputs, regional
knowledge contracts/worker/crop matching/source registry, year presentation,
calendar date/pagination contracts, transactional mutation patterns and the
explicit portability table whitelist. Confirmed that current knowledge requires
existing plants, seasonal kinds lack planting-method windows, and registry
coverage is limited to seven crop families/nine regional publications.

The proposal defines a compact timeline/wishlist and shared-modal experience,
future records independent of actual plants/accounting/care, ownership links
independent of planning completion, source-supported timing with manual fallback,
explicit-date projections/reminders, AI draft review and atomic create/link flows.
It describes eight proposed future implementation packets, their paths and
acceptance checks. They are not dispatched or claimed implemented by this task.

Executed document consistency/source-reference checks (review.json) plus
repository plan, packet, dispatch and boundary validators. No application unit,
integration/browser tests or deployment were required or claimed: only planning
artifacts changed. No production connection, account, data or runtime was touched.
