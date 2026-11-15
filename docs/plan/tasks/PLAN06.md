# PLAN06 — Planning integrations and opt-in reminders

Status: ACCEPTED.

### PLAN06 — Calendar, Garden year, Today and planning reminders

Depends: PLAN05.

Own: `server/internal/planning/`, `server/db/migrations/00022_planning_reminders.sql`, `server/db/queries/planning.sql`, `server/db/generated/` (generator only), `server/cmd/worker/main.go`, `server/internal/transport/graphql/planning_events.go`, `server/internal/transport/graphql/planning_events_http_test.go`, `server/internal/transport/graphql/gql/` (generator only), `server/internal/transport/graphql/limits.go`, `server/internal/transport/graphql/limits_test.go`, `contracts/graphql/planning.graphql`, `contracts/graphql/care.graphql`, `contracts/operations/planning.graphql`, `contracts/generated/` (generator only), `web/src/app/features/planning/`, `web/src/app/features/calendar/`, `web/src/app/features/seasons/seasons.ts`, `web/src/app/features/seasons/seasons.html`, `web/src/app/features/today/`, `web/src/app/features/inbox/`, `web/e2e/planning*.ts`, `web/playwright.planning.config.ts`, `docs/features/planting-planner.md`, `docs/plan/07-tasks.md`, `docs/plan/tasks/PLAN06.md`, `docs/plan/tasks/PLAN06-contract-change.md`, `.artifacts/agents/PLAN06/`. Integrator owns graph/ledger.

Acceptance: Add a separate workspace-scoped paginated action projection for at most 62 civil days, only explicit dates and active plans, never fake owned plants or recorded garden events. Separate Calendar filter/markers, optional dashed Garden year overlay and small Today section. Opt-in per-action in-app reminders reuse River and notifications, deduplicated by action/schedule revision/recipient. Bound each sweep; revalidate eligibility under parent lock before delivery, preserve notification/read history and civil dates on timezone changes. Existing care/email semantics remain intact.

Verify: Real PostgreSQL and HTTP scope/pagination/date-bound tests, reminder concurrency/restart/reschedule/cancellation/timezone tests; frontend filtering/unknown/error tests and actual desktop/phone browser flows; Node24 production build/units, Go regression/race, generation/lint/plan/boundary and full CI. Update the isolated matched API/worker preview and verify a real queued reminder job.
