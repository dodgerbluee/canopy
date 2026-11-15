# PLAN05 — Planning page and modal workflows

Status: IN_PROGRESS. Local verification passed; GitHub CI pending.

Added Planning beside Garden year on desktop and under mobile More, preserving
the existing bottom bar. The page has Planned/Wishlist views, a twelve-month
timeline, URL-bound popup filters, a horizon dialog, source/chosen distinctions,
compact chronological mobile rows and honest no-date/unsupported states. Reused
the existing design tokens, native choice controls, entry dialogs, focus/dirty
guards, theme defaults, exact currency parsing and GraphQL transport.

Name-only ideas, full intent editing, optional quantities/budgets, lifecycle,
cancel/restore, planned action creation/edit/removal and explicit source-backed
period selection work through real commands. Forms stay pristine on open;
ambiguous retries reuse their intention key. Accepted seasonal precision and
explicit anchor assumptions remain separate from real activity. Source groups
share a drawing only; all underlying notes remain available for review. Empty or
failed reads never masquerade as an empty garden.

The new plantingBoard query composes paginated existing plan aggregates and
bounded previews in one HTTP call, reusing hydrated rows and the pure engine.
Cursors bind workspace/filter/view/assumptions. Omitted assumptions reuse a
plan's saved choice only in its matching current context; explicit empty values
override them. Old site assumptions cannot move an accepted season or prevent
manual planning after geography changes.

Executed checks:

- Node24 production build and all 504 Angular tests pass, including fresh-form,
  exact/unknown/free money, coarse dates, invalid days, stable retry keys,
  network failure and late-response rejection (`final-web.log`). Component style
  budgets are unchanged and the new styles fit them.
- Actual PostgreSQL board/schedule tests and named HTTP operations passed,
  including private pages, bound cursors, unsupported ideas, explicit/default
  assumptions, no source calls on read and retained chosen dates
  (`final-board-tests.log`).
- Full Go regression with real PostgreSQL passed (`go-regression.log`); targeted
  race checks passed (`race-tests.log`).
- Twenty real-browser scenarios passed against the authenticated combined app:
  desktop/Android-sized phone, each in light/dark browser themes. Covers hidden
  URL filters, no overflow, pristine/draft protection, exact free estimate,
  create/edit/cancel, action CRUD, source precision confirmation and frost-relative
  preview/retained assumptions. Overview and source dialogs have zero axe
  violations (`browser-tests.log`, ignored screenshots/traces).
- Built and served the combined app with matched API/worker at
  http://127.0.0.1:7082/planning. API reads and writes used this actual deployment.
  Its worker completed planting.guidance jobs and produced READY regional facts
  for three sample crops, with an honestly UNSUPPORTED wishlist item. No source
  call was needed for calendar projection. A paired image refresh preserved
  preview plan/action/plant/photo counts (`preview-counts-before.txt`,
  `preview-counts-after.txt`).
- Final lint/typecheck/build, generation no-diff and plan/packet/dispatch/boundary
  checks passed (`lint.log`, `generation-no-diff.log`, `final-plan-boundary.log`).
  Final saved-assumption/board tests, race checks and full Go regression passed.
  GitHub CI pending.

Preview is isolated: canopy-planning-preview + canopy-planning-preview-db, their
own network and volumes; only 127.0.0.1:7082 is published. Its disposable login is
planner-preview / canopy-preview-2027. Local model settings were copied through a
private ignored env file; credentials are never in source/evidence. The real
4311 garden, remote production, their databases/configuration and photos were
not changed. The separate canopy-plan05-pg container is for automated tests only.

Remaining: PLAN06 integrations/reminders, PLAN07 atomic Add to garden/action
completion, PLAN08 MCP/assistant context and release gate. The user explicitly
authorized proceeding through the complete remaining implementation.
