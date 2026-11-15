# PLAN05 — Planning page

Status: ACCEPTED.

### PLAN05 — Angular Planning page and consistent modal workflows

Depends: PLAN04.

Own: `web/src/app/features/planning/`, `web/src/app/app.routes.ts`, `web/src/app/shell/`, `web/src/app/features/navigation/`, `web/e2e/planning*.ts`, `web/playwright.planning.config.ts`, `server/internal/planning/schedule_service.go`, `server/internal/planning/schedule_types.go`, `server/internal/planning/schedule_service_test.go`, `server/internal/planning/board.go`, `server/internal/planning/board_test.go`, `server/internal/transport/graphql/planning_board.go`, `server/internal/transport/graphql/planning_schedule.go`, `server/internal/transport/graphql/planning_board_http_test.go`, `server/internal/transport/graphql/limits.go`, `server/internal/transport/graphql/limits_test.go`, `server/internal/transport/graphql/gql/` (generator only), `contracts/graphql/planning.graphql`, `contracts/operations/planning.graphql`, `contracts/generated/` (generator only), `docs/features/planting-planner.md`, `docs/plan/07-tasks.md`, `docs/plan/tasks/PLAN05.md`, `docs/plan/tasks/PLAN05-contract-change.md`, `.artifacts/agents/PLAN05/`. Integrator owns graph/ledger.

Acceptance: Add desktop Planning navigation and mobile More without changing bottom navigation. Build a quiet responsive twelve-month view with clearly separate source and chosen bands, Planned/Wishlist, URL-bound hidden filters and compact mobile plans. Name-only ideas, full intent editing, exact optional budgets, lifecycle/cancel/restore, action CRUD and explicit source/window selection use shared entry dialogs and native choice controls. Keep fresh forms pristine, drafts protected, focus/scrolling comfortable and validation/conflicts/network retries honest. Reuse the pure Go engine; no client-side horticultural scheduling. Compose paginated rows and bounded previews in one read-only page request. Serve an isolated matched API/worker Docker preview with sample plans, leaving production untouched. No stub promotion, reminders or planning chat controls for later packets.

Verify: Meaningful typed data/form/store/component tests, cancellation/retry/conflict/unknown states, actual named API composition and cost bounds, Node24 production build/full units, desktop/mobile/both-theme browser/accessibility flows and overflow/focus checks. Actual authenticated Docker save/edit/action/confirmation and background-job proof; Go regression/race/generation/plan/boundary gates and full CI. Record preview URL and preserved production boundary.
