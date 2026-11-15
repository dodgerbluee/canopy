# PLAN06 — Planning integrations and opt-in reminders

Status: ACCEPTED. Local verification and full exact-source CI passed.

Calendar uses a separate typed, scoped and paginated 62-day explicit-action projection. Planning markers/filters do not require an owned plant and never count as recorded history. Garden year has an opt-in dashed chosen-window overlay; Today shows a bounded next-week section with honest service failures. Linked actions open their plan. Inbox routes PLANTING_ACTION notices to Planning and preserves explicit read state.

Reminders are off by default and require a chosen day on/before the action. The existing River worker inserts INAPP notifications at 09:00 in workspace time, with a seven-day catch-up horizon, at most 200 candidates per sweep and one action/schedule-revision/recipient key. Parent/workspace locks revalidate current eligibility, including cancellation, rescheduling, deletion and timezone changes. No future notification is prematerialized; delivered/read history remains intact. No email or actual care/activity is created.

Executed checks so far:
- Node24 production build and all 510 Angular tests passed (`web-verified.log`). Tests cover independent plant/planning filters, incomplete-page failures, unchanged civil dates and explicit reminder opt-in/date validation.
- Real PostgreSQL domain and named HTTP projection tests passed, including range/cursor/workspace isolation, unknown/coarse targets, cancellation and no actual-history writes. Reminder tests cover ten competing workers, DST-day gate, restart deduplication, rescheduling, read-history retention, cancellation after candidate selection, removed opt-in, timezone change and bounded catch-up (`projection-reminder-final.log`, final clean focused run).
- Targeted race tests passed (`race.log`).
- Migration CLI applied 22, rolled back to 21 and reapplied 22 on a disposable database (`migrations.log`). Existing enum label is deliberately retained on down; historical planning notifications are removed for rollback.
- Built/restarted matched API and worker in the isolated 7082 preview, preserving its data/storage/config. A real queued planning.reminders job completed and inserted one PLANTING_ACTION/INAPP notification (`worker-proof.txt`). Temporary action was removed; notification history retained. Ready endpoint returned 200. Original 4311 and remote production were not touched.
- Generation no-diff and plan/boundary checks passed (`generation-no-diff.log`, `plan-boundary.log`).

- All 32 authenticated desktop/phone × light/dark browser scenarios passed, including separate Calendar filters/counts, Today/deep links, optional Year overlay with zero axe violations, and Inbox explicit unread state (`browser.log`).
- Full Go regression passed with isolated PostgreSQL (`go-regression.log`); final Go vet, contracts typecheck and production build passed (`lint.log`).

Full GitHub CI passed for `82b3521d93ebd48bee99fbef1d190a98982e8d18`: https://github.com/dodgerbluee/canopy/actions/runs/43743143862 (generation, lint, all unit and real PostgreSQL integrations). Acceptance proceeds to PLAN07 and PLAN08.
