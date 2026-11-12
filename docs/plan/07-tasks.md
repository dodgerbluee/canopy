# Implementation tasks and dependency graph

All tasks start PLANNED. No feature is implemented by writing this backlog. Each task inherits the packet and completion rules in 06-delegation. Dependencies mean accepted commits, not merely started work. Paths are target ownership boundaries materialized by T03; each task also owns matching tests/fixtures. Shared generated files are produced in an integrator-owned generation step, not manual model edits.

Effort is relative: S = narrow, M = normal packet, L = must split after first file/behavior inventory. Do not translate model throughput into calendar promises. Estimated total 70–110 bounded coding/review packets after L tasks split; review/repair work additional. Gates favor correctness over one-shot generation.

## Foundation (contract freeze before feature coding)

### T00 — Toolchain and local-model compatibility spike [M, deep]

- Depends: none. Own: `docs/adr/0001-toolchain.md`, `docs/plan/tasks/T00.md`, ignored temporary spike artifacts. No application code or global OpenCode edits.
- Verify exact stable Angular/Node/TS/CDK/CLI Vitest compatibility; Go/gqlgen/sqlc/pgx/River/codegen compile together. Pin versions/digests/license inventory. Demonstrate libvips HEIC orientation + derivative GPS stripping with legal fixture.
- Benchmark Strata simple tool edit/test in disposable temp worktree with no secrets, valid JSON output, tool use, timeout and interruption behavior, fast/deep variants. Verify permissions policy actually limits writes; record inference throughput before proposing concurrency.
- Acceptance: exact reproduction commands/results; any failed assumption gets explicit architecture revision. Real model connectivity already checked, but tool-edit competence must be measured. No fake vision success.
- Verify: run tiny Go+TS compile tests, CLI model tool check, media decoding probe; save sanitized outputs. If unsupported codec/tool version, resolve here before downstream dispatch.

### T01 — GraphQL contracts [non-executable release group]

- Canonical executable nodes: T01.r1 (depends T00), T01.care (depends T20), T01.ai (depends T28). Own: `contracts/graphql/`, `contracts/operations/`, `contracts/fixtures/api/`; integrator owns generator config.
- T01.r1 freezes shared values plus collection/journal/purchases/media/portability SDL only; T01.care later freezes care/source/profile/notification operations; T01.ai later freezes analysis/insight/OCR operations. Each node splits further if needed. Fully define its release operation inventory in 03, typed unions/payloads, all inputs/enums/scalars, pagination/revisions/idempotency; no generic JSON result escape.
- Acceptance: every listed user journey has named operations; schema validates; all operations validate; null/unknown and error fixtures agree. Explicit scalar mappings preserve money and dates.
- Verify: schema parse/validate, operation validation, generated TS typecheck and gqlgen compile spike; negative cursor/revision/scalar fixtures. No schema changes by feature agents afterward without integrator gate.

### T02 — Data contracts [non-executable release group]

- Canonical executable nodes: T02.r1 (depends T01.r1), T02.care (depends T01.care), T02.ai (depends T01.ai). Own: `server/db/migrations/00001_*` through integrator-reserved range, `server/db/queries/`, `docs/adr/0002-data.md`.
- Freeze only matching release tables/queries in each node; R1 includes identity, acquisitions, collection, purchases, media, activity, audit/idempotency and portability. Care/AI use additive migrations before their feature lanes, not unused initial tables. Define tables/constraints/indexes from 03, workspace-composite FKs, audit/idempotency/projection invariants, exact currency/decimal handling. Reserve future migration numbers centrally.
- Acceptance: fresh DB builds, invalid cross-workspace links/allocations rejected through DB+atomic use cases where cross-row constraints require transaction checks; sqlc generation reproducible. Critical query EXPLAIN strategy documented.
- Verify: migrate blank/previous-stage schema, sqlc compile, FK/uniqueness/check constraints and concurrent allocation transaction prototype. No SQLite substitute.

### T03 — Repository, builds, codegen, CI and agent rules [L, deep]

- Depends: T00, T01.r1, T02.r1. Own: root configs/Makefile/AGENTS.md, base apps/server scaffolds, codegen, scripts/CI and test harness plumbing. Serialized integration lane.
- Create pinned Go/Angular skeletons, strict lint/type/build, generated artifacts, boundary checker, DB fixtures harness, Docker test helpers, all target make commands with implemented suites (future suite selectors explicitly pending, never success stubs).
- AGENTS.md enforces dependencies/imports/narrow scope/no secret access. Build a task-packet validator checking missing fields, accepted predecessors and allowed files; execution ledger template. No generic multi-agent swarm.
- Acceptance: fresh checkout builds/tests existing skeleton without AI/network keys; generation leaves no diff; a deliberate forbidden import fails; task with unmet dependency cannot dispatch.
- Verify: make check/test-unit/test-integration/build on clean temp checkout; packet validation positive/negative fixtures. CI smoke for Linux codec runtime included after T14.

### T04 — Backend composition, configuration and transaction seams [M, deep]

- Depends: T03. Own: `server/internal/platform/` except auth/media-specific adapters; `server/cmd/{api,worker,migrate}/`.
- Wire config, pgx pool, clock/ID ports, safe structured errors/logs, HTTP lifecycle/timeouts/limits, River registration and tx insertion seam. No speculative generic repositories.
- Acceptance: invalid/insecure production config stops startup; readiness/liveness differentiated; graceful shutdown bounded; tx rollback leaves neither state nor job.
- Verify: config matrix, httptest health/error cases, real DB+River atomic enqueue/rollback and restart test.

### T05 — Design tokens, accessible primitives and visual reference [L, deep]

- Depends: T03. Own: web `web/src/app/ui/`, visual fixture/story routes confined to dev/test at `web/src/app/stories/`, design token CSS at `web/src/styles/`.
- Split tokens/layout, forms/overlays, six composed screen references. Implement 04 exact visual direction with real legal fixture photos. Use CDK mechanics; no production API dependencies.
- Acceptance: themes, 3 viewport baselines, keyboard/dialog/focus, reduced motion, all primary components and representative errors. Review screenshots, do not approve from generated source alone.
- Verify: component interaction/contrast/axe tests and Playwright screenshots at specified viewports. No production fake data.

### T06 — Owner lifecycle, sessions, authorization and CSRF [L, deep]

- Depends: T04. Own: `server/internal/identity/`, `server/db/queries/`, `server/internal/transport/http/`, `server/cmd/admin/`.
- Split bootstrap/password/hash/session, endpoints+middleware+recovery. Implement production security contract and owner-scoped request principal. No unprotected demo login.
- Acceptance: login/rotation/expiry/revoke/recovery, no signup, CSRF+Origin, private media/export hooks, generic auth errors, safe audit.
- Verify: AUTH suite including cross-workspace principal IDs, concurrent session revocation, cookie modes, password recovery invalidates sessions.

### T07 — Angular shell, auth UX, routes and settings basics [M, deep]

- Depends: T05, T06. Own: web `web/src/app/core/auth/`, `web/src/app/shell/`, `web/src/app/features/` (login/overview/settings pages), app routes/config (`web/src/app/app.routes.ts`), e2e harness (`web/e2e/`, `web/playwright.e2e.config.ts`, `scripts/serve-dist.mjs`); server auth handling limited to the session-expiry problem code in `server/internal/transport/http/auth.go`; integrator serializes routing glue.
- Mobile/desktop nav, typed login/reactive forms, auth route guard plus server auth handling, onboarding timezone/currency/location unknown states. Starter collection opt-in only.
- Acceptance: refresh/deep-link/back/session expiry work, keyboard and responsive shell, no secrets in browser persistent storage.
- Verify: auth component/router tests + real login/logout/expired-session browser tests, both themes.

### T08 — Typed GraphQL transport and contract harness [M, deep]

- Depends: T01.r1, T04, T06, T07. Own: `server/internal/transport/graphql/` (base handler/error/scalars/loaders, incl. generated `server/internal/transport/graphql/gql/`); web `web/src/app/core/graphql/`, shared generated fixtures.
- Implement generated-document HTTP wrapper with CSRF/errors/cancellation, precise scalar mappings, page store conventions; API complexity/body/depth/alias controls. No normalized caching framework.
- Acceptance: a named query and mutation round-trip from generated TS to gqlgen; auth/payload errors differentiated; aborted navigation does not update destroyed store.
- Verify: contract fixtures, HttpClient tests, real HTTP GraphQL tests, MONEY scalar boundaries, over-limit alias/page/depth rejection. Query limits charged on requested page size.

## R1 — trustworthy journal

### T09 — Collection domain and backend [M, deep]

- Depends: T02.r1, T06, T08. Own: `server/internal/collection/`, `server/db/queries/` + regenerated `server/db/generated/`, collection resolver files in `server/internal/transport/graphql/` (`collection_resolvers.go`, `resolver.go` wiring, test harness).
- Plant/location CRUD, unknown acquisition provenance, group counts, context history, archive/restore, tags, revision and idempotency. Profile/care hooks via explicit application interface only; scheduling behavior connected in T24.
- Acceptance: tree identity distinct, unknowns retained, archived plant read history persists, cross-workspace context links denied, cycle/depth limit works.
- Verify: use-case tests + collection API/DB integration, stale update race and duplicate create replay.

### T10 — Collection UI and plant overview [M, deep]

- Depends: T05, T08, T09. Own: web `web/src/app/features/collection/` (grid/list, filters, add/edit/location/context forms, archive/restore states), plant detail identity/context header components under the same feature folder, route registration glue in `web/src/app/app.routes.ts` (integrator-serialized), e2e specs for the collection journey.
- Photo-led grid/list, filters in URL, add/edit/location/context forms, archive/restore states. Wire real operations, no inline fake services.
- Acceptance: add five tree/two group starter opt-in, user can leave cultivar/count unknown, errors and conflicts actionable, keyboard mobile flow.
- Verify: component/store tests; create/edit/archive deep-link Playwright journey and visual states.

### T11 — Purchases and exact allocation backend [L, deep]

- Depends: T09. Own: `server/internal/purchases/`, `server/internal/money/`, purchase SQL/resolvers, money value package.
- Split money/allocator, transactional purchase commands/acquisition pricing, spending read model. Receipt associations integrated after T15; purchase remains usable without files.
- Acceptance: 03 $100 example, overhead rounding/unallocated supplies, known vs unknown spending, multi-currency grouping, one receipt does not multiply totals.
- Verify: MONEY suite plus unknown/free/missing/acquisition-to-shared-priced fixture transitions, fuzz conservation/rounding properties, concurrent revisions, DB rollback midway through allocation; linked allocation edit/reassign/delete vs priceAcquisition races and explicit unlink projection updates.

### T12 — Purchase and expense UI [M, deep]

- Depends: T05, T08, T11. Own: web `web/src/app/features/purchases/`.
- Line/allocation editor, derived preview exact decimal math, seller/date, reconciliation and currency display. Server is authoritative; no float totals.
- Acceptance: accessible table/form mobile reflow, shared receipt placeholder slot until T32, invalid total reports exact discrepancy, conflict preserves entered draft.
- Verify: MONEY UI boundary values, reconciliation and two-fig expense browser journey; empty/unknown/multi-currency visual fixtures.

### T13 — Upload reservation, private ObjectStore and streamed ingest [M, deep]

- Depends: T04, T06, T08. Own: `server/internal/platform/media/` (Store port +
  filesystem adapter), `server/internal/media/` (reserve/finalize service,
  sniff, sanitizer, cursors, sweeper), `server/internal/transport/http/uploads.go`
  (upload HTTP handler), `server/db/queries/assets.sql` +
  `server/db/generated/` (asset SQL), `server/internal/transport/graphql/media_resolvers.go`
  + `server/internal/transport/graphql/resolver.go` (resolvers),
  `server/internal/transport/graphql/media_http_test.go`,
  `server/internal/platform/config.go`, `server/internal/platform/jobs.go`,
  `server/cmd/api/main.go`.
- Implement reserve/stream/finalize lifecycle, quota/size/hash/opaque keys, tx queue link, failure/orphan states. No image decoding inside API request.
- Acceptance: duplicate/truncated/cross-owner uploads safe, concurrent quota reservations cannot exceed quota, temp paths not user controlled, atomic rename/fsync behavior documented.
- Verify: MEDIA stream/reservation tests with real filesystem and DB, upload interrupted after file-before-DB boundary.

### T14 — Phone photo normalization worker [L, deep]

- Depends: T00, T13. Own: `server/internal/media/processor/` (plan, keys,
  exif, sandbox, pipeline, worker + tests), `docker/media.Dockerfile`
  (pinned processing container), `server/testdata/media-corpus/` (fixtures +
  SHA256SUMS), `server/db/queries/assets.sql` + `server/db/generated/`
  (GetAssetByID, MarkAssetStateFrom), `server/internal/platform/config.go`
  (media worker config), `server/cmd/worker/main.go` (worker wiring),
  `Makefile` (media-image target).
- Split codec sandbox, processing lifecycle/derivatives. JPEG/PNG/WebP/HEIC/HEIF decode, orientation, responsive derivatives, safe metadata extraction/GPS stripping. Strict process resource limits.
- Acceptance: actual iPhone-shaped HEIC test, 12MP performance baseline, corrupt/bomb/timeout failure safe, retried job writes same deterministic variants, deleted asset cannot revive.
- Verify: MEDIA corpus, subprocess/container limit tests, crash after derivative creation, Linux runtime codec build smoke. T00 spike alone does not replace runtime tests.

### T15 — Authorized media delivery and attachment operations [M, deep]

- Depends: T09, T11, T14. Own: `server/internal/transport/http/media.go`
  (delivery handler), `server/internal/media/links.go` +
  `server/internal/media/purge.go` (link use cases + grace purge),
  `server/db/queries/photos.sql`, `server/db/queries/receipts.sql`,
  `server/db/queries/assets.sql` + `server/db/generated/`,
  `server/internal/transport/graphql/media_resolvers.go` +
  `collection_resolvers.go` + `purchases_resolvers.go` (resolvers),
  `server/internal/transport/graphql/links_http_test.go`.
- Fixed variant downloads, ownership/headers/ETag, receipt PDF attachment-only, cover uniqueness, links and cleanup reference rules. Photos/capture date semantics from 03.
- Acceptance: user can attach processed photo/receipt only in owner workspace; unauthenticated originals unavailable; deleting one link preserves shared asset; GPS-free display derivative.
- Verify: MEDIA headers/auth tests, cover race unique constraint, purchase receipt association rollback, wrong-purpose asset rejection.

### T16 — Photo metadata, chronology and compare query backend [M, deep]

- Depends: T15. Own: photo metadata/journal projection operations, photo resolver/read queries.
- TakenAt/date-source/viewpoint/caption editing, query chronological photo series, compare input validation, cover selection and tombstones.
- Acceptance: unknown naive EXIF offset prompts UI data rather than invented instant; capture-date correction updates projection atomically; comparing different plants requires explicit collection compare mode (deferred, reject in v1).
- Verify: photo ordering/date fixtures, timezone ambiguity, concurrent edit/deletion and paginated series.

### T17 — Activities and measurements backend [M, deep]

- Depends: T09. Own: `server/internal/journal/commands.go` (activity/measurement
  commands + CompletionPort), `server/db/queries/journal.sql`
  (projection queries), journal mutation resolvers in
  `server/internal/transport/graphql/`.
- Typed care/observation/measurement records, units, backdating, revisions and voiding; transactional timeline projection. Expose completion integration port for T23.
- Acceptance: water/fertilizer units validated, original measurements retained, corrections audit preserved, voided events absent from active projections.
- Verify: JOURNAL use-case/DB/API tests and unit conversions/property tests; duplicate activity idempotency.

### T18 — Unified timeline, filters and search backend [M, deep]

- Depends: T11, T16, T17. Own: journal read model/resolvers, collection search queries/index migration reserved by integrator.
- Mixed typed timeline, allocation-aware purchase entries, stable cursor/filter contract, bounded collection search and query batching.
- Acceptance: one timeline includes all R1 record types, monetary totals not derived from cards, query count bounded, unknown dates explicit.
- Verify: JOURNAL paging/filter/type union API suite, 50-plant <=8-query test, seeded EXPLAIN/performance. No unconditional trigram infrastructure.

### T19 — Journey UI, quick log and measurement view [M, deep]

- Depends: T08, T10, T17, T18. Own: web `web/src/app/features/journal/`, `web/src/app/features/collection/plant-detail.ts`, `web/src/app/app.routes.ts`, `web/src/app/shell/shell.ts`, `web/src/app/shell/shell.spec.ts`, `web/e2e/journal.spec.ts`, `web/playwright.e2e.config.ts`.
- Type filters, chronological groups, typed cards, quick activity/measurement entry/correction, source links, load-more and empty/error states.
- Acceptance: log event from phone, backdate/edit/void, reload timeline correct; generated union exhaustiveness compile check; unknown info not zero-filled.
- Verify: component timeline states and JOURNAL browser journey, no duplicated IDs across load-more refresh.

### T20 — Growth comparison UI [M, deep]

- Depends: T05, T16, T19, T32.a. Own: `web/src/app/features/collection/compare-photos.ts`, `web/src/app/features/collection/compare-photos.spec.ts`, `web/src/app/app.routes.ts`, `web/src/app/features/collection/plant-detail.ts` (compare entry link), `web/e2e/capture.spec.ts` (compare leg).
- Two-photo selection, aspect-fit views, keyboard slider optional, date captions/deep-link, mismatch viewpoint label.
- Acceptance: two uploaded photos across dates compare at phone/desktop sizes, deleted selection recovers, reduced motion honored, no fabricated growth metrics.
- Verify: comparison route/component/keyboard tests and visual baselines.

## R2 — contextual care

### T21 — Reviewed knowledge profiles and source provenance [L, deep]

- Depends: T09, T17, T01.care, T02.care. Own: `server/internal/care/` (care.go, validate.go, sources.go, profiles.go, import.go, starter/starter.json, care_test.go), `server/db/queries/care.sql` + generated care queries. Source/profile GraphQL resolvers and `contracts/fixtures/care` land with T24 when care.graphql joins the server schema.
- Split schema/publish lifecycle and six category starter profiles. Research reliable regional extension guidance; save licensed summaries/source refs with applicability, uncertainty and review date. Unknown region leaves sensitive rules inactive.
- Acceptance: profiles cover citrus (species/cultivar distinctions), fig, strawberry bearing types, blackberry cane types; owner can review/edit/retire; no general “strip all trees” rule. Owner-entered sources possible without scraping service.
- Verify: profile schema/source validation, version immutability and pinning tests; human review of applicability. Literature sources are data work with provenance, not AI-generated facts.

### T22 — Pure care engine and recurrence math [L, deep]

- Depends: T21. Own: `server/internal/care/engine/` (date.go, recurrence.go, cursor.go, context.go, notify.go, engine_test.go, testdata/fuzz regression seeds); no DB/network/HTTP imports (test-enforced).
- Split calendar recurrence and contextual advice. Implement exactly supported recurrence, missing context, date windows, covered/indoor behavior; reasons/evidence/missing-fact outputs.
- Acceptance: deterministic outputs for fixed inputs, no unconditional watering from elapsed days/rain, sensitive unknowns abstain, complete/skip/snooze cadence rules agree with 03.
- Verify: CARE table/property/fuzz tests including DST/leap/year/hemisphere; independent reviewer scrutinizes edge cases before scheduler integration.

### T23 — Durable occurrence generation and activity completion [L, deep]

- Depends: T04, T17, T22. Own: `server/db/migrations/00005_care_scheduler.sql`, `server/db/queries/care.sql` + `server/db/generated/care.sql.go`/`models.go` (plan/occurrence/cursor queries, outage collapse, upsert-revive insert), `server/internal/care/occurrences.go`, `server/internal/care/scheduler.go` (River care.tick worker + periodic startup catch-up), `server/internal/care/occurrence_test.go`, `server/internal/journal/commands.go` (linked-completion guard), `server/internal/platform/jobs.go` (periodic seam), `server/cmd/worker/main.go` (worker wiring).
- Split materializer/catch-up and complete/skip/snooze/correct-completion transactions. Unique logical keys, plan revision regeneration, history preservation, completion+activity atomicity; persisted scheduler cursor.
- Acceptance: process kill/duplicate tick safe, outage yields one actionable interval check rather than missed-day flood, backdated completion regenerates only eligible future work.
- Verify: CARE real DB/River concurrency, restart/catch-up, completion double-click, undo before/after later completion, linked-activity correction rejection, tx failure and timezone edit preview tests.

### T24 — Care GraphQL application surface and lifecycle integration [M, deep]

- Depends: T08, T09, T21, T23. Own: `contracts/graphql/care.graphql` (journal CARE union/enum wiring), `contracts/operations/journal.graphql` (`... on CareEntry`), `contracts/generated/graphql.ts`, `server/gqlgen.yml`, `server/db/queries/care.sql` + `server/db/generated/care.sql.go` (plant-scoped cancels w/ resolved_at, notification dedup insert, ListOccurrencesByIDs), `server/db/queries/plant_contexts.sql` + generated (clock_timestamp current-context fix), `server/internal/care/reads.go`/`notify.go`/`hooks.go`/`notify_test.go` + additions to `occurrences.go`/`validate.go`/`scheduler.go`, `server/internal/collection/collection.go` (tx-scoped Hook interface)/`plants.go` (hook call sites)/`hooks_test.go`, `server/internal/journal/journal.go`/`commands.go` (exported ProjectActivity/TombstoneActivity), `server/internal/transport/graphql/care_resolvers.go`/`care_http_test.go`/`resolver.go`/`journal_resolvers.go`/`collection_resolvers.go` + `gql/` regen, web `src/app/features/journal/journal-view.ts`/`journal-timeline.ts` (CareEntry exhaustiveness), `server/internal/media/processor/sandbox.go`/`pipeline_docker_test.go` (cross-lane --rm-removal race fix).
- Agenda/profile/plan operations, revision handling, archive cancels future work, restore regenerates, context edit invalidates affected future advice; plan preview operation in accepted SDL.
- Acceptance: typed actionable reason, source/version and missing inputs exposed; task action mutation cannot use another plant's occurrence.
- Verify: CARE API operations with real DB, archive/context mutation transaction rollback and stale suggestion prevention hooks.

### T25 — Care agenda, plan editor and task interactions [M, deep]

- Depends: T05, T24. Own: web/src/app/features/care/{care-view,care-agenda,plan-editor,care-tab}.ts(+spec), web/src/app/{app.routes,shell}.ts, web/src/app/features/collection/plant-detail.ts, web/src/app/features/journal/journal-timeline.ts, contracts/operations/care.graphql (PlantCarePlan/CareProfilePicker/CareProfileDetail), contracts/generated/graphql.ts, web/e2e/care.spec.ts, web/playwright.e2e.config.ts (care-desktop project).
- Grouped agenda, source explanation, recurrence preview, complete/skip/snooze, timezone preview, profile version upgrade review.
- Acceptance: check/action visibly distinct, due/snoozed/original dates clear, no schedule guess when cultivar context missing.
- Verify: CARE browser journey with fixed clock, plan revision conflict, next-five-dates UI and mobile/a11y snapshots.

### T26 — Weather provider, freshness and context evaluation [M, deep]

- Depends: T04, T22, T24. Own: `server/internal/care/weather/` (adapter +
  fixtures `server/testdata/weather/`), `server/db/migrations/00006_weather_evidence.sql`,
  weather queries in `server/db/queries/care.sql`, `server/internal/care/weather_worker.go`,
  engine weather wiring in `server/internal/care/occurrences.go`/`reads.go`,
  evidence mapping in `server/internal/transport/graphql/care_resolvers.go`,
  weather knobs in `server/internal/platform/config.go`, worker wiring in
  `server/cmd/worker/main.go`.
- Open-Meteo mapping and provenance, optional opt-in location, timeout/rate/cache, stale unavailable semantics, covered-pot exclusions. No geolocation permission prompt on page load.
- Acceptance: provider failure preserves manual care; no rain-based automatic completion; exact snapshot referenced by advisory result.
- Verify: recorded HTTP fixtures and tiny opt-in public API smoke, stale/indoor/covered fixtures, no network in default tests.

### T27 — Notification inbox and email delivery [M, deep]

- Depends: T06, T23, T24. Own: server/db/migrations/00007_notification_delivery.sql; server/db/queries/care.sql + server/db/generated/; server/internal/email/email.go; server/internal/care/notify.go, email_worker.go, email_worker_test.go, care.go, occurrences.go; server/internal/platform/config.go; server/cmd/worker/main.go; web/src/app/features/inbox/inbox.ts + inbox.spec.ts; web/src/app/app.routes.ts; web/src/app/shell/shell.ts.
- In-app inbox always; SMTP optional; unique daily digest intent, local-time scheduling/catch-up, read state, delivery attempts and bounded retry. External email can be delivered twice after crash-after-send; disclose at-least-once channel, stable Message-ID best effort (not exactly-once promise).
- Acceptance: duplicate ticks do not create duplicate intents, quiet default daily digest, no secret/media in email, SMTP failure visible without blocking care.
- Verify: CARE DST/digest tests, fake SMTP integration, send-success-before-ack failure, in-app read UI test.

### T28 — Today overview and contextual dashboard [M, deep]

- Depends: T10, T19, T25, T26, T27. Own: contracts/graphql/overview.graphql; contracts/operations/overview.graphql; server/db/queries/care.sql (LatestWorkspaceWeather) + server/db/generated/; server/internal/transport/graphql/overview_resolvers.go + overview_http_test.go; server/internal/transport/graphql/journal_resolvers.go (shared journalPage); web/src/app/features/today/today.ts + today.spec.ts; web/e2e/today.spec.ts; web/src/app/stories/today.ts; web/src/app/app.routes.ts; web/src/app/shell/shell.ts; signIn-heading ripple in existing e2e specs.
- Compose actual due tasks, recent journey, optional conditions, proposals slots. Keep queries bounded; no invented activity or charts.
- Acceptance: useful zero-provider/zero-photo mode and populated polished mode; quick actions update agenda and journal coherently.
- Verify: real stack today→complete→journal journey, stale weather/empty/error visual scenarios, initial load budget.

## R3 — assistance with evidence

### T29 — Analysis ports, jobs, capability and budget foundation [L, deep]

- Depends: T04, T08, T18, T24, T01.ai, T02.ai. Own: `server/internal/insights/` (`insights.go`, `service.go`, `worker.go`, `snapshot.go`, `validator.go`, `provider.go` + `insights_test.go`, `worker_test.go`, `validator_test.go`); AI adapter configuration and SQL: `server/internal/platform/config.go` (+`config_test.go`), `server/db/queries/analysis_support.sql` + generated `server/db/generated/analysis_support.sql.go`; analysis GraphQL surface: `server/gqlgen.yml`, `server/internal/transport/graphql/analysis_resolvers.go` + `analysis_http_test.go`, `resolver.go` wiring, regenerated `server/internal/transport/graphql/gql/`; composition roots `server/cmd/api/main.go`, `server/cmd/worker/main.go` (disclosed additions).
- Split capability/schema/budget and transactional runs/retry/cancel. Text-only Strata adapter optionally reused only after capability check; no image claims. Configurable endpoints, no arbitrary fetch/tool calls, opt-in data sharing.
- Acceptance: no-provider mode, duplicate semantic key, budget reservation/refund, bounded context, generation-fenced stale results, request cancel and status polling.
- Verify: AI lifecycle real DB/fake HTTP provider suite, malformed/schema/injection/oversized/timeout/429, cost ceiling race including charged timeout/retry/repair/cancel/UTC-month boundary, change/delete during provider call.

### T30 — Identification and receipt extraction drafts [L, deep]

- Depends: T11, T15, T29. Own: `server/internal/insights/router.go` (Router port + BuildRouter), `receiptocr.go`, `plantnet.go`, `t30_test.go`; dispatch wiring in `server/internal/insights/{provider,worker}.go`; `server/db/queries/analysis_support.sql` + regenerated `server/db/generated/analysis_support.sql.go`; `server/internal/platform/config.go` + `config_t30_test.go`; composition wiring `server/cmd/{api,worker}/main.go`; `docker/media.Dockerfile` + Makefile media target (canopy-media r1→r2 adds tesseract-ocr+poppler-utils per ADR-0001 pins); r1→r2 refs in `server/internal/media/processor/{sandbox_test,pipeline_docker_test}.go`, `server/internal/transport/graphql/links_http_test.go`. No new GraphQL surface (owner confirmation rides existing `updatePlant`/`createPurchase`).
- Split identification candidates and OCR-to-editable-purchase draft. Use local Tesseract 5 with sandboxed Poppler PDF rasterization, plus optional configured text model for structuring OCR text; T00 pins/probes binaries. No external OCR requirement. No unverified provider assumption.
- Acceptance: names remain candidates until owner selection; cultivar not invented; extracted totals editable/reconciled; original receipt preserved, instructions in receipt cannot change system behavior.
- Verify: AI/receipt corpus with rotated/ambiguous receipts, two-plant totals, malformed data, no provider and owner confirmation API.

### T31 — Vision observations and longitudinal insight engine [L, deep]

- Depends: T16, T18, T21, T26, T29. Own: server/internal/insights/vision.go (OpenAI-compatible image chat port, pinned comparison prompt, cost unknown), server/internal/insights/photos.go (shared adapterPhotoBytes: variants/bounds/transcode/mime), evidence provenance server/internal/insights/snapshot.go canopy-snap/2 + db/queries/analysis_support.sql (photo sha256+taken_at) + regenerated db/generated, CANOPY_VISION_* surface internal/platform/config.go + insights/router.go + cmd/api + cmd/worker wiring, tests internal/insights/t31_test.go + t31_live_test.go (live-gated) + internal/platform/config_t31_test.go, spike .artifacts/agents/T31/SPIKE.md.
- Split actual vision adapter/capability and cross-record synthesis. Use an OpenAI-compatible image-capable chat adapter with explicit configured model; capability spike documents privacy, supported image format, cost and limits. No locally available vision model assumed. Store selected evidence IDs/hash/source/model/prompt versions; distinguish observation, hypothesis, missing facts, task proposal.
- Acceptance: compare actual pair with configured vision provider, abstain on incompatible context/unscaled growth, no unsupported source refs, no direct care writes.
- Verify: AI adversarial/stale/reference tests; live evaluated sample later T35. If no vision provider available, task is BLOCKED for capability, not accepted with a text mock.

### T32 — Capture and extraction UX [non-executable release group]

- Canonical executable nodes: T32.a (depends T12,T15,T16), T32.b (depends T32.a,T29,T30). Own: `web/src/app/features/capture/` (`upload.ts`, `photo-queue.ts`, `receipt-upload.ts` + specs), `web/src/app/features/collection/plant-detail.ts`, `web/src/app/features/purchases/purchase-form.ts` + spec, `web/e2e/capture.spec.ts`, `web/e2e/fixtures/receipt.png`, `web/e2e/global-setup.mjs`, `web/playwright.e2e.config.ts`, `scripts/e2e_with_worker.sh`; T32.b delivered capability-gated Extract + RequestAnalysis/poll in `receipt-upload.ts`, draft review card + exact one-shot apply in `purchase-form.ts`, and defect fix `server/internal/insights/receiptocr.go` (PNG sandbox self-copy) + `server/internal/insights/t30_test.go` regression found by the e2e journey.
- Split resilient photo queue and receipt/OCR review. Per-item upload/processing/retry, explicit capture date fallback/timezone, portrait mobile capture, original-download link and external sharing consent.
- Acceptance: mixed multi-upload successes retained, no manual file path entry, optional OCR doesn't block manual purchases, capability unavailable clear.
- Verify: MEDIA/AI browser flow, interrupted upload/retry/reload, form draft, no sensitive storage, screenshots both themes.
- Note: core capture subpacket T32.a depends only T12/T15/T16 and may ship in R1. OCR/request subpacket T32.b adds T29/T30 and ships in R3. This split is mandatory to avoid gating R1 on AI.

### T33 — Cost/care/history synthesis and suggestion acceptance backend [M, deep]

- Depends: T11, T17, T24, T29, T31. Own: `server/internal/insights/` (snapshot.go canopy-snap/3 cost sections, validator.go causal guard, worker.go commit-tx projection, suggestions.go, suggest_jobs.go, t33_test.go), `server/db/queries/analysis_support.sql` + `analysis.sql` flagging + `server/db/generated/`, `server/internal/care/hooks.go` + `profiles.go` flag wiring + `t33_flag_test.go`, `server/cmd/worker/main.go` sweeper registration, `contracts/graphql/analysis.graphql` union/enum extends + `contracts/operations/journal.graphql` InsightEntry fragment + generated, `server/internal/transport/graphql/analysis_resolvers.go` + `journal_resolvers.go`.
- Explain spending by currency and patterns in recorded care with qualified language. Proposal acceptance rechecks source/context versions and creates exactly one explicit task; expired/stale cannot accept.
- Acceptance: costs match purchases, no assumed causal diagnosis from correlation, duplicate accept replays same task, action not falsely logged completed.
- Verify: AI + MONEY integration, acceptance race, context/profile changed after analysis, source retired, ownership tests.

### T34 — Insight evidence UX and health review [M, deep]

- Depends: T20, T25, T31, T32.b, T33. Own: web `src/app/features/insights/**` (insights tab + suggestion card), plant-detail Insights tab wiring, Today open-suggestions slot in `src/app/features/today/**`, e2e insights journey `web/e2e/insights.spec.ts`, stub provider `web/e2e/stub-provider.mjs`, harness `scripts/e2e_with_worker.sh` + `web/e2e/global-setup.mjs` + `web/playwright.e2e.config.ts`.
- Structured observations/hypotheses/source sheet, evidence photo comparison links, missing context, progress/cancel/retry, acceptance preview, dismissal, capability/budget states.
- Acceptance: owner can trace every claim to referenced evidence or see unsupported warning; approved task appears in care, manual observations usable without AI.
- Verify: AI browser cases including no vision/budget/stale/cancel/duplicate accept, keyboard screenshot review.

### T35 — Real model evaluation and privacy/budget pilot [L, deep + independent reviewer]

- Depends: T30, T31, T33, T34. Own: `tests/ai-eval`, sanitized evidence/results; no architecture changes during evaluation.
- Assemble 30 legal labeled cases/rubric, run actual selected text/vision/OCR models within approved test budget, judge source fidelity/abstention/usefulness and failure states.
- Acceptance: 05 evaluation gates met; document model/version/input/privacy/price and sample limits. If gates fail, repair owning task and rerun affected evaluation; do not approve enabled feature on fake fixtures.
- Verify: deterministic validator suite plus opt-in `make test-ai-live`; human-capable reviewer checks images/labels, text-only Strata cannot be sole pathology judge.

## R4 — release quality, portability and operations

### T36 — Security/concurrency/performance hardening [L, deep]

- Depends: T28, T34. Own: `tests/performance`, threat-model tests, package fixes strictly routed through owning tasks.
- Split auth/input/media/provider adversarial testing and performance/query load. Measure target dataset and resource limits; threat-model exact deployed shape, no imaginary SaaS tenancy scope.
- Acceptance: 05 budgets met or explicitly revised with evidence before release; no open exploitable high/critical; query amplification and idempotency races tested.
- Verify: scan + race + load + file attack tests, logs redaction, real resource enforcement. Audit source code dependency paths, not just green scanner.

### T37 — Accessibility, visual polish and browser compatibility [L, deep + visual reviewer]

- Depends: T28, T34. Own: browser/accessibility/visual tests; UI corrections assigned to owners to avoid global cleanup scope.
- Chromium/Firefox/WebKit current supported versions, mobile touch layout, keyboard/focus/zoom/screen-reader smoke, themes/reduced motion. Refresh screenshot baselines with before/after evidence.
- Acceptance: WCAG 2.2 AA critical journeys, no layout overflow at 360px/200% zoom, coherent typography/density, all error states.
- Verify: axe + manual keyboard/screen-reader checklist + Playwright browsers + screenshot review. Do not claim automated axe proves full accessibility.

### T38 — Portability [non-executable release group]

- Canonical executable nodes: T38.a (depends T04,T06,T11,T15,T17), T38.final (depends T38.a,T23,T33). Own: T38.a — `server/internal/portability/` (service, export worker, backup/restore, maintenance, seal, `RUNBOOK.md`, `manifest.schema.json`, tests), `server/db/migrations/00002_portability.sql`, `server/db/queries/portability.sql`, `server/internal/transport/http/exports.go`, `server/internal/transport/graphql/export_resolvers.go`, `server/internal/collection/plants.go` (purge matrix) + `purge_test.go`, `server/cmd/admin/main.go` (backup/restore CLI), Makefile `backup`/`restore-test` targets; T38.final adds care/insight dump coverage and configured backup destinations later.
- Split R1 export/core restore (only accepted R1 schema exists at this point); complete all care/insight table coverage after T23/T33 before acceptance. Maintenance snapshot/quiesce, encrypted backup/retention, safe archive paths, checksum manifest, session invalidation, job recovery, owner purge policy.
- Acceptance: clean restore reconciles every scoped entity and file hash; financial allocations unchanged; no secret/session in user export; no permanent erase of shared receipt by one plant purge.
- Verify: PORTABILITY real fresh stack plus failed backup/unquiesce, invalid archive/path traversal, partial file failure, restore timing. Final acceptance depends additionally T23/T33 so R3 data isn't omitted; R1 subpacket independent.

### T39 — Production-shaped deployment and observability [M, deep]

- Depends: T04, T06, T14, T27, T38.final. Own: infra containers/compose/proxy, release runbooks, telemetry dashboards/alerts.
- Nonroot API/worker, immutable images/digests, TLS routing, private DB/media, config/secrets, migration ordering, health/queue/disk/backup indicators. Compose volumes and worker image include tested codecs.
- Acceptance: blank-host setup, safe recovery/upgrade/rollback documented, providers optional, backup target status visible, no public DB or insecure production cookie fallback.
- Verify: fresh deploy smoke + previous-release migration simulation + worker/process failure + readiness/alert tests. No actual public deployment is authorized by this planning task.

### T40 — Full release acceptance rehearsal [M, deep]

- Depends: T35, T36, T37, T38.final, T39. Own: release report/evidence, no unreviewed implementation patches.
- Execute all R1–R4 journeys on production-shaped stack with optional APIs both absent/present, exact fixture collection, real tested AI capability; user-like phone capture to care/insight/cost/export loop.
- Acceptance: all required make verify checks, measured budgets, restore, all task ledger evidence, no unreachable fake-only features. Every deviation explicit.
- Verify: fresh install then upgrade/restore journey; independent rerun commands/logs. Failed checks route to owning tasks and repeat affected regression.

### T41 — Fresh independent implementation review and A gate [M]

- Depends: T40. Reviewer gets repository/evidence/rubric only, no conversation or implementer defense. This is separate from current planning review. Own: review artifacts under `.artifacts/agents/T41/` only; no product-code edits.
- Grade actual code architecture, behavior, test credibility, UX, reliability, maintainability, privacy. Inspect real screenshots/runtime and representative code/tests, not just docs.
- Acceptance: >=90/100, no P0/P1, no rubric section <70%; concrete findings fixed in bounded packets and independently reassessed until A. Preserve review history and unresolved caveats.
- Verify: reproduction spot checks selected by reviewer; reviewer cannot waive failing requirement to produce desired grade.

### UI01 — Owner-approved official Angular UI migration [M–L, deep]

- Depends: T07, T12, T20, T25, T27, T28, T32.a. Explicitly authorized 2026-11-02 by owner following approval of the standalone mock. This is an additional task, not an early acceptance of T37.
- Own: `web/src/styles/`, `web/src/styles.css`, `web/src/app/shell/`, `web/src/app/ui/`, `web/src/app/features/`, `web/src/app/stories/`, `web/e2e/`, `.artifacts/agents/UI01/`, `docs/plan/tasks/UI01.md`, `docs/plan/04-experience.md`. Integration records also own this task heading, UI01 graph status, T37 dependency and its ledger row.
- Preserve existing API/contracts, typed forms, uploads, revision/conflict behavior, real care semantics and unrelated ongoing backend work. No mock records, stock photography, fabricated weather or AI results in production. UI-only migration unless a proven integration defect demands a separately documented change.
- Acceptance: official routed Angular UI reflects approved mock's forest sidebar, warm canvas, typography, photographic collection/detail/journal and responsive navigation; all existing shipped functions remain reachable; actual uploaded photos drive hero/covers, absence uses useful local placeholders; light/dark accessible controls; loading/empty/error/conflict states preserved.
- Verify: pinned Node24 production build and all Angular unit tests; boundary/plan/dispatch checks; targeted browser journeys over isolated real stack and/or typed network fixtures; desktop/tablet/mobile screenshots, keyboard and axe checks; no production deployment or restart of unrelated active services.

## Delivery order and concurrency lanes

Critical start: T00 → T01.r1 → T02.r1 → T03 → T04/T05 → T06/T07 → T08 → T09.

R1 work: collection T10; purchases T11→T12; media T13→T14→T15→T16→T32.a; activities T17; then T18→T19→T20 and T38.a. **R1 exit:** T06–T20 + T32.a + T38.a accepted (T21+ not needed).

R2: T01.care→T02.care→T21→T22→T23→T24→T25; T26/T27; T28. **R2 exit:** T21–T28 plus R1.

R3: T01.ai→T02.ai→T29→T30/T31→T32.b→T33→T34→T35. **R3 exit:** T29–T35 plus R2.

R4: T36/T37/T38.final→T39→T40→T41. T38.a and deployment scaffolds may start earlier; final acceptance includes latest full schema.

These are logical independent lanes, not authorization to overload one Strata endpoint. Default serialize model coding. When measured capacity permits two: DB/backend lane vs feature UI lane only after corresponding schema is frozen; do not parallelize migrations, codegen, package locks, app routes, or shared design tokens.

## Task ledger schema

`id, release, status, baseCommit, dependsOn, packetPath, allowedPaths, modelRef, worktree, attempts, implementationCommit, reviewerSession, testEvidence, visualEvidence, deviations, acceptedBy`.

Canonical nodes and explicit dependencies are already in `task-graph.json`; `validate_plan.py` checks DAG and release isolation now. Parent groups T01/T02/T32/T38 cannot dispatch; completion means all their executable children accepted. T03 extends this planning validator with packet/commit/evidence validation. Validate DAG including suffix tasks, not the informal range descriptions. No task can be accepted while a prerequisite failed. T38 final uses distinct subtask ID and dependencies so no cycles are introduced by early backup work.

### UI02 — consistent modal data entry

Depends: UI01, T35 (ACCEPTED).

Own: `web/src/app/ui/`, `web/src/app/core/`, `web/src/app/features/`, `web/src/app/shell/`, `web/src/app/app.routes.ts`, `web/src/app/stories/routes.ts`, `web/src/styles/`, `web/e2e/`, `docs/plan/tasks/UI02.md`, `.artifacts/agents/UI02/`. Integrator owns task graph, ledger and experience notes. No backend, contracts/generated files, dependencies or user services.

Acceptance: Every shipped garden data-entry flow uses the same accessible modal treatment; consistent spacing/labels/hints/errors/actions in both themes and phone/desktop. Owner clarification: retain native choice fields inside the modal forms; no custom picker replacement. Preserve native/reactive form semantics, exact money, revision guards, upload recovery, analysis budget/review consent, navigation/deep links. Dirty-dismiss protection, busy-close guard, focus trapping/restoration and scroll containment shared. Existing workflows remain reachable and tested; no fake data or backend changes.

Verify: isolated Node24 production build + all Angular units; full existing real-stack frontend journeys adjusted only for new presentation; targeted picker/modal/dirty/focus/browser tests; both themes/phone/desktop screenshots + axe; boundary/plan/packet checks; actual Docker preview.

### UI03 — Today diagnostics and Journal spacing

Depends: UI02.

Own: `web/src/app/features/today/`, `web/src/app/features/journal/`, `.artifacts/agents/UI03/`, `docs/plan/tasks/UI03.md`, `server/internal/transport/graphql/ui03_schema_test.go`. Integrator owns graph/ledger. Repair reported live failure without changing backend contracts; improve Journal page and timeline spacing, preserving modal data-entry behavior and native controls.

Acceptance: Today frozen query is accepted by the live current server schema; the stale API is replaced without deleting owner records or uploads. Journal heading, filters, cards and actions use consistent spacing at mobile/tablet/desktop. Modal behavior and native choices preserved.

Verify: Node24 production build and frontend units; Journal fixture browser checks at 390/768/1440; exact Today operation schema and real HTTP/Postgres regression checks; API readiness, both UI endpoints and active executable identity; boundary/plan/packet checks.

### DATA01 — Owner photo archive and provenance import

Depends: UI03.

Own: `.artifacts/agents/DATA01/`, `docs/plan/tasks/DATA01.md`. Integrator owns task graph and ledger. No application runtime source changes.

Acceptance: All owner-provided plant folders imported without duplicates; original photo bytes and EXIF capture instants preserved; purchase/acquisition provenance and image observations distinguish confirmed facts from uncertainty. Use normal authenticated API mutations; no direct database writes or credential-source access without explicit approval.

Verify: Inventory/digest/date validation; API input schema checks; persisted plant/photo/purchase/journal readback; media derivatives and cover delivery; repeat/resume behavior.

### UI04 — Plant photo framing

Depends: UI03.

Own: `web/src/app/ui/photo.ts`, `web/src/app/ui/photo.spec.ts`, `web/src/app/ui/photo-framing.ts`, `web/src/app/ui/photo-framing.spec.ts`, `web/src/app/ui/plant-card.ts`, `web/src/app/features/today/today.html`, `web/src/app/features/collection/plant-detail.html`, `web/src/app/features/journal/journal-timeline.html`, `.artifacts/agents/UI04/`, `docs/plan/tasks/UI04.md`. Integrator owns graph, ledger, experience notes.

Acceptance: Plant card and hero crops favor actual canopy/foliage instead of trunks; archive gallery and journal show full original composition; original media/date/cover identity retained. Local, bounded image analysis with safe fallbacks, correct resizing and lifecycle cleanup. No new provider/data contracts or dependencies.

Verify: Required Node24 build and units; actual imported image browser comparisons on phone/desktop and grid/list/hero; synthetic sparse/no-foliage, portrait/landscape focus geometry, image error/recovery and ResizeObserver cleanup checks; boundary/plan/packet checks; update live Docker preview.

### UI05 — Collection quick filters behind one Filter button

Depends: UI04.

Own: `web/src/app/features/collection/collection.ts`, `web/src/app/features/collection/collection.html`, `web/src/app/features/collection/collection.css`, `web/src/app/features/collection/collection.spec.ts`, `web/e2e/collection.spec.ts`, `.artifacts/agents/UI05/`, `docs/plan/tasks/UI05.md`. Integrator owns graph, ledger.

Acceptance: Collection toolbar shows only inline search (with inline clear) and one Filter button carrying an active-filter count badge; status/category/order/view live in a slide-out drawer with backdrop, ×/Escape/backdrop-click close and focus return; deep-linkable URL params and live-apply semantics unchanged; axe clean; e2e journey updated through the drawer and a unit test covers the toggle + badge.

Verify: Node24 web units; focused collection e2e journeys incl. archive→filter→restore through the drawer; axe on /plants; full `make check` on the merged tree; owner screenshots of closed/open/mobile states.

### UI06 — Canopy brand favicon

Depends: UI05.

Own: `web/public/favicon.svg`, `web/public/favicon.ico`, `web/src/index.html`, `.artifacts/agents/UI06/`. Integrator owns graph, ledger.

Acceptance: Browser tab shows the Canopy brand mark (sprout glyph on accent tile, brand colors), SVG for modern browsers with a regenerated multi-size .ico fallback; no stale default icon remains.

Verify: Node24 production build; served-icon delivery check on the live preview; plan gates.

### UI05 — Uniform filters and Add Plant card

Depends: UI04.

Own: `web/src/app/features/collection/collection.ts`, `web/src/app/features/collection/collection.html`, `web/src/app/features/collection/collection.css`, `web/src/app/features/collection/collection.spec.ts`, `web/e2e/collection.spec.ts`, `.artifacts/agents/UI05/`, `docs/plan/tasks/UI05.md`. Integrator owns graph, ledger and experience notes.

Acceptance: Collection filter side panel uses consistent native fields/spacing/actions and keyboard focus; Add Plant action is one outlined dashed tile after plant rows matching Docked’s pattern. Existing filtering URL state, pagination, modal route, archival operations and smart photo framing preserved.

Verify: Node24 build/unit checks; browser fixtures grid/list/empty/filtered/error/loading, filters/keyboard/reset/focus and Add Plant modal navigation; phone/desktop themes; scoped formatting and boundary/plan/packet checks; both local frontends updated.

### UI07 — Choice controls and garden navigation

Depends: UI05.

Own: `web/src/app/ui/select.ts`, `web/src/app/ui/dialog.ts`, `web/src/app/ui/dialog.spec.ts`, `web/src/app/features/collection/collection.ts`, `web/src/app/features/collection/collection.html`, `web/src/styles/select.css`, `web/src/styles/base.css`, `web/src/app/features/today/hero-rotation.ts`, `web/src/app/features/today/hero-rotation.spec.ts`, `web/src/app/features/today/today.ts`, `web/src/app/features/today/today.spec.ts`, `web/src/app/features/collection/plant-form.ts`, `web/src/app/shell/shell.html`, `web/src/app/shell/shell.spec.ts`, `.artifacts/agents/UI07/`, `docs/plan/tasks/UI07.md`. Integrator owns graph, ledger and experience notes.

Acceptance: Consistent modern native select treatment including theme/focus/disabled and supported native popup; Your garden opens collection; Today rotates eligible uploaded covers per visit without changing on in-page actions, uses recorded plant name. Preserve forms/payloads/accessibility/crops and no provider/dependency/backend changes.

Verify: Node24 build and units; browser Add Plant six choices and popup/keyboard/reactive values/dirty dismissal; garden-link destination; Today consecutive visits and refresh stability; phone/desktop themes + axe; publish Docker preview and primary.

### UI08 — Compact purchase entry with amount cascades

Depends: UI07. Owner-directed: purchase edits were failing and the form was
called out as ugly/scroll-heavy with three independently editable amount
fields that always tripped conservation validation.

Own: `web/src/app/features/purchases/purchase-form.ts`,
`web/src/app/features/purchases/purchase-form.spec.ts`,
`web/e2e/purchases.spec.ts`, `web/e2e/collection.spec.ts`,
`server/db/migrations/00008_purchase_edit_fk.sql`,
`server/db/migrations/00009_purchase_line_position.sql`,
`server/db/queries/purchases.sql`, `server/db/generated/`,
`server/internal/purchases/create.go`, `server/internal/purchases/query.go`,
`server/internal/transport/graphql/purchases_http_test.go`,
`.artifacts/agents/UI08/`, `.artifacts/agents/REPAIR-02/`,
`.artifacts/agents/REPAIR-03/`. Integrator owns graph and ledger.

Acceptance: Editing a purchase whose allocation has a KNOWN acquisition link
saves (REPAIR-02: FK `ON DELETE NO ACTION DEFERRABLE`; unlink/protection still
refuse true removals). Amount entry is concise and self-consistent: single-
allocation lines sync line total ↔ allocation item both directions; header
total pushes down (exactly one line) and up (Σ lines + tax + shipping);
header tax/shipping distribute across allocations proportional to item weights
(largest remainder, exact BigInt minor units); per-allocation overhead inputs
hidden when header overhead is blank. Receipt fields sit in one compact grid
row, lines are bordered cards, recon bar compact — no wasted space. Lines and
allocations display in entry order across create and edit (REPAIR-03:
position columns, composite keyset cursor). All existing control ids/labels
and contract semantics preserved.

Verify: Go integration on real PG17 (REPAIR-02 repro fails-before/passes-
after; TestPurchaseLineOrderPreservedOverHTTP); purchases unit suite;
purchases e2e journey extended with an edit phase (date-only edit saves,
line-total edit cascades to sole allocation + header, ledger/void follow);
full `make check` ALL GATES PASSED; rebuild + restart localhost:4300 preview
and host API per owner rule.

### UI09 — Receipt-first purchase entry

Depends: UI08.

Own: `web/src/app/features/purchases/purchase-form.ts`, `web/src/app/features/purchases/purchase-form.spec.ts`, `web/src/app/features/capture/receipt-upload.ts`, `web/src/app/features/capture/receipt-upload.spec.ts`, `.artifacts/agents/UI09/`, `docs/plan/tasks/UI09.md`. Integrator owns graph and ledger.

Acceptance: Fresh purchase opens without validation errors. Receipt upload is first and available before saving, extraction remains explicit and reviewable, all readable lines populate a blank draft with exact money arithmetic. Attach staged receipts after successful purchase creation; partial attachment failure preserves the saved purchase identity for retry without duplicate purchases. Manual entry, edited values, amount cascades, dirty guards and existing saved-purchase uploads preserved.

Verify: Node24 purchase/upload units and production build; receipt-first browser journey with mocked API covering upload/extraction/apply/save/attach and partial-failure retry; phone/desktop themes and accessibility; plan/packet/boundary gates; publish local frontends.

### UI10 — Receipt extraction recovery

Depends: UI09.

Own: `AGENTS.md`, `web/src/app/features/capture/receipt-upload.ts`, `web/src/app/features/capture/receipt-upload.spec.ts`, `server/internal/insights/receipt_pipeline_test.go`, `.artifacts/agents/UI10/`, `docs/plan/tasks/UI10.md`. Integrator owns graph and ledger.

Acceptance: Explicit retry of a terminal failed/stale/cancelled extraction requests a fresh run with forceRerun and a new stable attempt key; uncertain transport and active-run polling preserve their existing attempt identity. Distinguish terminal outcomes and unsaved purchase honestly. Live worker runs current code with canonical shared storage configuration. No edits to owner records or receipt originals.

Verify: Node24 unit/build/browser retry coverage; Go real PostgreSQL and Docker OCR receipt pipeline; local queued job processed by refreshed worker; plan/packet/boundary checks; publish both frontends.

### UI11 — Floating Account dropdown

Depends: UI10.

Own: `web/src/app/ui/menu.ts`, `web/src/app/ui/menu.spec.ts`, `web/src/styles/base.css`, `.artifacts/agents/UI11/`, `docs/plan/tasks/UI11.md`. Integrator owns graph and ledger.

Acceptance: Account popup floats below its trigger without resizing header or moving page content; right aligned and viewport constrained in both themes. Click outside, Escape, selection and keyboard Tab dismiss appropriately; keyboard focus/navigation/actions preserved. No backend/dependency changes.

Verify: Node24 menu/shell units and production build; browser header/page geometry unchanged on open, outside/escape/selection/focus and viewport checks on phone/desktop themes; axe; publish both local frontends.

### UI12 — Vision receipt extraction

Depends: UI10.

Own: `AGENTS.md`, `server/internal/insights/receiptvision.go`, `server/internal/insights/receiptvision_test.go`, `server/internal/insights/vision.go`, `server/internal/insights/router.go`, `server/internal/insights/t30_test.go`, `server/internal/platform/config.go`, `server/internal/platform/config_test.go`, `server/cmd/api/main.go`, `server/cmd/worker/main.go`, `README.md`, `.artifacts/agents/UI12/`, `docs/plan/tasks/UI12.md`. Integrator owns graph and ledger.

Acceptance: Configured vision model reads receipt images using actual image content, returns validated editable drafts with unknown fields preserved; existing local OCR/PDF fallback remains when appropriate, failures never silently downgrade. Local deployment capability disclosure configurable; no GraphQL changes. Canonical API/worker settings aligned; verify actual Covington receipt without creating purchase or modifying original.

Verify: Go fake-server content/purpose/workspace/limits/error/repair tests, existing insights/config regressions, real Strata receipt test and queued worker extraction, matching runtime restarts; API capability provenance and both local frontends healthy.

### UI13 — Receipt detail accuracy and review status

Depends: UI12.

Own: `server/internal/insights/router.go`, `server/internal/insights/vision.go`, `server/internal/platform/config.go`, `server/internal/platform/config_test.go`, `server/cmd/api/main.go`, `server/cmd/worker/main.go`, `README.md`, `server/internal/insights/receiptvision.go`, `server/internal/insights/receiptimages.go`, `server/internal/insights/receiptimages_test.go`, `server/internal/insights/receiptvision_test.go`, `web/src/app/features/purchases/purchase-form.ts`, `web/src/app/features/purchases/purchase-form.spec.ts`, `web/src/app/features/purchases/receipt-review.ts`, `web/src/app/features/purchases/receipt-review.spec.ts`, `.artifacts/agents/UI13/`, `docs/plan/tasks/UI13.md`. Integrator owns graph and ledger.

Acceptance: Native vision receives bounded enlarged sections of long receipts with overview; actual Covington date and readable item names checked as well as exact amounts. Generic parsing/prompt only, no hardcoded vendor/date/items. UI distinguishes review/unknown fields from arithmetic mismatches, explains selected currency when not printed, preserves unsaved form and original receipt. No GraphQL changes or automatic purchase writes.

Verify: Go image-plan/bounds/provider regression tests, actual receipt accuracy through real Strata; Node24 units/build and browser review cases (balanced overhead, mismatch, incomplete/unknown, currency) on phone/desktop; pair backend refresh and publish local UI; plan gates.

### UI14 — Compact purchase line items

Depends: UI13.

Own: `web/src/app/features/purchases/purchase-form.ts`, `.artifacts/agents/UI14/`, `docs/plan/tasks/UI14.md`. Integrator owns graph and ledger.

Acceptance: Purchase line fields, allocation rows and secondary actions use less space with coherent styling; description gets adequate width, mobile controls remain comfortable, native selectors and all purchase behavior preserved.

Verify: Node24 focused purchase tests and production build; browser before/after line height, add/remove/split editing, phone/desktop themes, no horizontal overflow and accessibility; publish both local instances.

### UI15 — Mobile garden rounds

Depends: UI14.

Own: `web/src/app/features/rounds/`, `web/src/app/features/capture/upload.ts`, `web/src/app/features/capture/upload.spec.ts`, `web/src/app/app.routes.ts`, `web/src/app/shell/shell.ts`, `web/src/app/shell/shell.html`, `web/src/app/shell/shell.spec.ts`, `.artifacts/agents/UI15/`, `docs/plan/tasks/UI15.md`. Integrator owns graph and ledger.

Acceptance: Mobile-friendly page walks active plants with photo-for-today and watering actions. Correct plant/date attribution, explicit saved/error states, same-request idempotent retries and no duplicate tap saves, native camera input with chooser fallback, no guessed water volume. Existing upload transport reused; action/navigation races safe; existing routes accessible and shared style consistent. No backend or frozen contract changes.

Verify: Node24 meaningful state/transport/page/shell regression tests; production build; fixture browser phone/desktop themes, action/navigation/retry, timestamps, error/empty/history states, accessibility and no overflow. Publish local instances, preserve existing records during testing.

### UI16 — Direct plant navigation for rounds

Depends: UI15.

Own: `web/src/app/features/rounds/rounds.ts`, `web/src/app/features/rounds/rounds.html`, `web/src/app/features/rounds/rounds.css`, `web/src/app/features/rounds/rounds-navigation.css`, `web/src/app/features/rounds/rounds.spec.ts`, `.artifacts/agents/UI16/`, `docs/plan/tasks/UI16.md`. Integrator owns graph and ledger.

Acceptance: Compact photo thumbnails select any plant directly with a clear active state. Navigation stays reachable on mobile scroll; content clears the fixed bottom app navigation including safe area. Existing photo/water behavior and capture-target isolation preserved.

Verify: Focused Node24 rounds tests/build; phone/desktop theme browser action regression, arbitrary plant jumps, many plants, scrolling/safe-area geometry, missing-image fallbacks and accessibility. Publish existing local/phone instances.

### UI17 — Separate rounds grid and plant screen

Depends: UI16.

Own: `web/src/app/features/rounds/`, `web/src/app/app.routes.ts`, `.artifacts/agents/UI17/`, `docs/plan/tasks/UI17.md`. Integrator owns graph and ledger.

Acceptance: Rounds opens a full-page photo grid without an open plant. A tile navigates to a separate authenticated full-screen plant action route. Back returns to the grid and restores place; no simultaneous grid/detail rendering. Deep links, unknown plants, theme/auth expiry, existing photo/water saves and background retry behavior remain correct.

Verify: Node24 rounds/router tests and production build; browser list/detail exclusivity, Back/browser-back, deep links, long list position, phone/desktop themes, error/fallback/empty states, existing action retries and accessibility. Publish current local/phone previews.

### UI18 — Live photo save status

Depends: UI17.

Own: `web/src/app/features/rounds/rounds-actions.ts`, `web/src/app/features/rounds/rounds-actions.spec.ts`, `web/src/app/features/rounds/rounds.spec.ts`, `.artifacts/agents/UI18/`, `docs/plan/tasks/UI18.md`. Integrator owns graph and ledger.

Acceptance: Photo saving/saved/error state reacts on the existing plant screen without navigating away. Published state snapshots have fresh identity; private retry intents stay stable. Existing target/session/URL cleanup semantics preserved.

Verify: Reproduce stale same-screen status in an executed regression; Node24 rounds tests, production build and browser delayed-save/error/retry without leaving the screen. Publish local/phone instances.

### UI19 — Garden calendar and manual area rain

Depends: UI18.

Own: `contracts/graphql/calendar.graphql`, `contracts/operations/calendar.graphql`, `contracts/generated/`, `server/gqlgen.yml`, `server/db/migrations/00010_rain_events.sql`, `server/db/queries/calendar.sql`, `server/db/generated/`, `server/internal/journal/calendar.go`, `server/internal/journal/calendar_test.go`, `server/internal/transport/graphql/calendar.go`, `server/internal/transport/graphql/calendar_test.go`, `server/internal/transport/graphql/gql/`, `server/internal/portability/`, `web/src/app/features/calendar/`, `web/src/app/app.routes.ts`, `web/src/app/shell/shell.ts`, `web/src/app/shell/shell.html`, `web/src/app/shell/shell.spec.ts`, `web/src/app/ui/icon.ts`, `.artifacts/agents/UI19/`, `docs/plan/tasks/UI19.md`, `docs/plan/tasks/UI19-contract-change.md`. Integrator owns graph and ledger.

Acceptance: Calendar tab has responsive month view, plant visibility sidebar/filter, watering/photo/other icons and independently visible area rain. Photos use date added, other entries their recorded day in workspace timezone. Add rain modal accepts selected date and optional amount/note; unknown amount remains unknown; no watering/completion effects. Tenant isolation, complete pagination, idempotent retry, removable mistaken rain, honest errors and existing entry conventions preserved.

Verify: Disposable PostgreSQL service/transport coverage, generation, Node24 units/production build, browser desktop/phone themes/filter/month/rain/accessibility; migration and paired local runtime refresh with worker evidence, publish all local previews; plan/packet/boundary checks.

### UI20 — Calendar photo capture dates

Depends: UI19.

Own: `contracts/graphql/calendar.graphql`, `contracts/generated/`, `server/db/queries/calendar.sql`, `server/db/generated/`, `server/internal/journal/calendar.go`, `server/internal/transport/graphql/calendar_test.go`, `server/internal/transport/graphql/gql/`, `web/src/app/features/calendar/`, `.artifacts/agents/UI20/`, `docs/plan/tasks/UI20.md`. Integrator owns graph and ledger.

Acceptance: Calendar dates photos by their recorded capture timestamp, not upload time. Unknown/upload-fallback dates are not presented as capture dates. UI says photos taken. Rain/watering semantics preserved.

Verify: Real PostgreSQL GraphQL capture-versus-upload, DST, unknown-date and deletion tests; generated bindings; focused Node24 units/build/browser; paired local API/worker refresh and queued-job proof; publish desktop/phone.

### UI21 — Regional knowledge and seasonal year tracks

Depends: UI20.

Own: `contracts/graphql/knowledge.graphql`, `contracts/operations/knowledge.graphql`, `contracts/generated/`, `server/gqlgen.yml`, `server/db/migrations/00011_knowledge.sql`, `server/db/queries/knowledge.sql`, `server/db/generated/`, `server/internal/knowledge/`, `server/internal/transport/graphql/knowledge.go`, `server/internal/transport/graphql/handler.go`, `server/internal/transport/graphql/knowledge_test.go`, `server/internal/transport/graphql/resolver.go`, `server/internal/transport/graphql/gql/`, `server/internal/platform/config.go`, `server/internal/platform/config_test.go`, `server/cmd/api/main.go`, `server/cmd/worker/main.go`, `server/internal/portability/`, `web/src/app/features/seasons/`, `web/src/app/features/settings/`, `web/src/app/features/calendar/calendar.html`, `web/src/app/features/calendar/calendar.ts`, `web/src/app/features/calendar/calendar-switch.ts`, `web/src/app/app.routes.ts`, `web/src/app/shell/`, `web/src/app/ui/icon.ts`, `web/src/app/ui/entry-dialog.ts`, `web/src/app/ui/entry-dialog.spec.ts`, `web/src/styles/tokens.css`, `README.md`, `.artifacts/agents/UI21/`, `docs/plan/tasks/UI21.md`. Integrator owns graph and ledger.

Acceptance: Adding/editing a plant queues background region-aware knowledge enrichment without slowing plant creation. Explicit garden country/region/location and opt-in, no timezone-to-zone inference; city/postal search and manual zone override. Extensible regional source registry, permitted bounded scraping, citations/provenance, reused profiles and honest unknown/unsupported/error states. Rounded annual plant tracks show sourced growth/bloom/harvest/care/dormancy windows and overlap, detail modals and grounded garden question answering. Existing plants can be enriched; no guessed actual observations or automatic watering/completions. Export/restore and deletion respect new data.

Verify: Disposable PostgreSQL lifecycle/isolation/stale/queue/cache tests, fake HTTP extraction/source validation, regional routing/unknowns, real configured source/model smoke, typed contracts/generation/Go checks, focused Node24 tests/build/browser phone/desktop/themes/a11y; paired runtime refresh and actual queued-job proof; publish local previews.

### UI22 — Whole-garden seasonal overview

Depends: UI21.

Own: `web/src/app/features/seasons/`, `.artifacts/agents/UI22/`, `docs/plan/tasks/UI22.md`. Integrator owns graph and ledger.

Acceptance: One merged annual garden timeline highlights monthly care and seasonal windows across filtered plants, with unique plant counts, month selection, compact care grouping and modal source drill-down. Existing per-plant tracks remain. Missing timing and hidden filters do not invent tasks or completion status. Responsive light/dark, keyboard access and consistent modals.

Verify: Node24 aggregation/component regressions and production build; browser desktop/phone themes, filter propagation, month/group/source navigation, a11y and no overflow; publish existing previews without backend restart.

### UI23 — Complete source-backed timing ingestion

Depends: UI22.

Own: `server/internal/knowledge/`, `server/db/queries/knowledge.sql`, `server/db/generated/`, `server/internal/transport/graphql/knowledge_test.go`, `.artifacts/agents/UI23/`, `docs/plan/tasks/UI23.md`, `README.md`. Integrator owns graph and ledger.

Acceptance: Source timing preserves complete compound windows and discrete crop seasons; partial evidence or summary/timing mismatches trigger bounded repair or explicitly untimed notes. No widening from unrelated advice or unsupported cultivar claims. Versioned extraction invalidates old document/profile caches, automatically queues stale active opted-in plants, and hides stale guidance during refresh. Preserve records and existing consent/isolation/generation fencing.

Verify: Deterministic full/partial sentence, compound/range/season/relative regressions; fake-model repair and fallback; real PostgreSQL stale cache/requeue/HTTP tests; real source/model pomegranate/fig results; paired current API/worker refresh with actual queued job evidence and unchanged owner record counts.

### UI24 — Android LAN photo upload compatibility

Depends: UI23.

Own: `web/src/app/features/capture/`, `.artifacts/agents/UI24/`, `docs/plan/tasks/UI24.md`. Integrator owns graph and ledger.

Acceptance: Existing-photo selection, reselection/recovery and receipt uploads work without secure-context-only Web Crypto APIs over LAN HTTP. Finalize with the validated server-computed PUT checksum, consistent with rounds; malformed receipts fail clearly before finalization. Preserve file metadata, server validation, retry isolation and photo-date provenance.

Verify: Node24 affected upload/photo/receipt/rounds tests and production build. Real insecure-origin Android browser emulation for existing gallery photo, reselect and receipt upload; prove SubtleCrypto absent, full transport/finalize/attach, metadata and malformed-checksum handling. Publish desktop/phone previews; no backend restart.

### UI25 — Purposeful navigation, year, care and AI surfaces

Depends: UI24.

Own: `web/src/app/core/request-id.ts`, `web/src/app/features/insights/`, `web/src/app/shell/`, `web/src/app/app.routes.ts`, `web/src/app/features/navigation/`, `web/src/app/features/assistant/`, `web/src/app/features/settings/`, `web/src/app/features/seasons/`, `web/src/app/features/calendar/`, `web/src/app/features/journal/`, `web/src/app/features/care/`, `web/src/app/features/inbox/`, `.artifacts/agents/UI25/`, `docs/plan/tasks/UI25.md`. Integrator owns graph and ledger.

Acceptance: Year is its own first-class route/tab with old deep links preserved. Account menu has no duplicate navigation/theme controls, themes stay in Settings, all destinations remain reachable on mobile via dedicated More page. AI hub exposes grounded garden chat and per-plant photo insights. Care shows sourced seasonal/conditional guidance separately from real scheduled tasks; honest setup explanation. Journal gains plant scoping and clear memory intent; Inbox has actionable destinations without implicit read mutations.

Verify: Node24 targeted navigation, chat, journal, care, settings and inbox tests/build; browser desktop/phone themes, menu/More reachability, deep links, empty/error states, sources, AI actions and a11y; publish existing instances.

### UI26 — Bring-your-own AI connections

Depends: UI25.

Own: `contracts/graphql/ai-connections.graphql`, `contracts/operations/ai-connections.graphql`, `contracts/generated/`, `server/gqlgen.yml`, `server/db/migrations/00012_ai_connections.sql`, `server/db/queries/ai_connections.sql`, `server/db/generated/`, `server/internal/aiconnection/`, `server/internal/insights/`, `server/internal/knowledge/`, `server/internal/platform/config.go`, `server/internal/platform/config_test.go`, `server/internal/transport/graphql/ai_connections.go`, `server/internal/transport/graphql/ai_connections_test.go`, `server/internal/transport/graphql/handler.go`, `server/internal/transport/graphql/resolver.go`, `server/internal/transport/graphql/gql/`, `server/cmd/api/main.go`, `server/cmd/worker/main.go`, `server/internal/portability/`, `web/src/app/features/settings/`, `web/src/app/features/assistant/`, `.artifacts/agents/UI26/`, `docs/plan/tasks/UI26.md`, `README.md`. Integrator owns graph and ledger.

Acceptance: Editable named server-stored AI connections and model/feature assignments, OpenAI-compatible and native provider adapters, real bounded connection test, write-only encrypted credentials, no keys in reads/logs/browser storage/exports. Environment settings stay valid fallback. API and worker resolve saved workspace connections consistently without restart, preserve consent and existing record semantics. Safe endpoint validation and explicit hosted/local disclosure.

Verify: Protocol fixtures, encrypted storage/tenant isolation/revision/idempotency/key-keep/clear/redaction tests, actual Strata connection/test and queued worker proof, portability exclusions, typed generation, UI tests/browser modal forms/errors and phone reachability; paired local deployment.

### UI27 — Original photo capture dates

Depends: UI26.

Own: `contracts/graphql/schema.graphql`, `contracts/generated/`, `server/internal/media/`, `server/internal/transport/graphql/media_resolvers.go`, `server/internal/transport/graphql/photo_dates_test.go`, `server/internal/transport/graphql/media_http_test.go`, `server/internal/transport/graphql/gql/`, `web/src/app/features/capture/`, `web/src/app/features/rounds/`, `.artifacts/agents/UI27/`, `docs/plan/tasks/UI27.md`. Integrator owns graph and ledger.

Acceptance: Optional automatic capture-date resolution on photo attachment uses original EXIF DateTimeOriginal and its offset (or explicitly supplied timezone); never file modification/upload time as capture. Manual values win. Android gallery, rounds and recovered uploads use automatic resolution. Missing metadata remains visibly unknown/upload fallback. Journal/calendar use the resolved instant. Correct the reported existing fallback photo using original metadata, with optimistic revision and journal projection.

Verify: Bounded/malformed TIFF metadata parsing, offsets/naive timestamps, backend original-byte resolution and journal/calendar ordering, frontend retry/recovery and Android HTTP upload tests; actual original-file diagnosis, paired runtime restart/queued worker and local previews.

### MCP00 — MCP integration implementation plan

Depends: UI27.

Own: `docs/plan/09-mcp-implementation.md`, `docs/plan/tasks/MCP00.md`, `.artifacts/agents/MCP00/`. Integrator owns graph and ledger.

Acceptance: Source-verified Grok Bot/Hermes compatibility, architecture grounded in current Go services/session auth, explicit tool and permission contracts, photo capture-date and care/financial invariants, test strategy, and sequential implementation packets. Planning only: no MCP listener, credential or public tunnel created.

Verify: Read relevant code/contracts and official client/protocol/SDK documentation; validate internal source links, task DAG and packet evidence.

### M01 — Contracts and SDK compatibility

Depends: MCP00.

Own: `contracts/mcp/`, `server/internal/agentapi/catalog/`, `server/go.mod`, `server/go.sum`, `docs/adr/0013-mcp.md`, `scripts/gen_mcp.py`, `.artifacts/agents/M01/`, `docs/plan/tasks/M01.md`. Integrator owns graph and ledger.

Acceptance: Implement step 1 of `09-mcp-implementation.md`; record limitations honestly and preserve existing domain/security semantics.

Verify: Relevant Go/PostgreSQL/SDK/Node24/browser checks with executed evidence; final paired API/worker and local preview verification in M12.

### M02 — Agent grants and credentials

Depends: M01.

Own: `server/internal/agentaccess/`, `server/db/migrations/00013_agent_access.sql`, `server/db/queries/agents.sql`, `server/db/generated/`, `.artifacts/agents/M02/`, `docs/plan/tasks/M02.md`. Integrator owns graph and ledger.

Acceptance: Implement step 2 of `09-mcp-implementation.md`; record limitations honestly and preserve existing domain/security semantics.

Verify: Relevant Go/PostgreSQL/SDK/Node24/browser checks with executed evidence; final paired API/worker and local preview verification in M12.

### M03 — Shared operations and audit attribution

Depends: M02.

Own: `server/internal/agentaccess/`, `server/internal/agentcontext/`, `server/internal/agentapi/`, `server/internal/transport/graphql/agent_executor.go`, `server/internal/transport/http/auth.go`, `server/internal/collection/`, `server/internal/journal/`, `server/internal/media/`, `server/internal/care/`, `server/internal/purchases/`, `server/internal/insights/`, `server/internal/knowledge/`, `server/internal/portability/`, `server/db/migrations/00014_agent_operations.sql`, `server/db/queries/agents.sql`, `server/db/generated/`, `.artifacts/agents/M03/`, `docs/plan/tasks/M03.md`. Integrator owns graph and ledger.

Acceptance: Implement step 3 of `09-mcp-implementation.md`; record limitations honestly and preserve existing domain/security semantics.

Verify: Relevant Go/PostgreSQL/SDK/Node24/browser checks with executed evidence; final paired API/worker and local preview verification in M12.

### M04 — MCP HTTP transport

Depends: M03.

Own: `server/internal/transport/mcp/`, `server/cmd/api/main.go`, `server/internal/platform/config.go`, `server/internal/platform/config_test.go`, `.artifacts/agents/M04/`, `docs/plan/tasks/M04.md`. Integrator owns graph and ledger.

Acceptance: Implement step 4 of `09-mcp-implementation.md`; record limitations honestly and preserve existing domain/security semantics.

Verify: Relevant Go/PostgreSQL/SDK/Node24/browser checks with executed evidence; final paired API/worker and local preview verification in M12.

### M05 — Garden read tools

Depends: M04.

Own: `server/internal/agentapi/`, `contracts/mcp/`, `.artifacts/agents/M05/`, `docs/plan/tasks/M05.md`. Integrator owns graph and ledger.

Acceptance: Implement step 5 of `09-mcp-implementation.md`; record limitations honestly and preserve existing domain/security semantics.

Verify: Relevant Go/PostgreSQL/SDK/Node24/browser checks with executed evidence; final paired API/worker and local preview verification in M12.

### M06 — Agent settings UI

Depends: M05.

Own: `server/cmd/api/main.go`, `contracts/graphql/agents.graphql`, `contracts/operations/agents.graphql`, `contracts/generated/`, `server/gqlgen.yml`, `server/internal/transport/graphql/agents.go`, `server/internal/transport/graphql/resolver.go`, `server/internal/transport/graphql/gql/`, `web/src/app/features/settings/`, `.artifacts/agents/M06/`, `docs/plan/tasks/M06.md`. Integrator owns graph and ledger.

Acceptance: Implement step 6 of `09-mcp-implementation.md`; record limitations honestly and preserve existing domain/security semantics.

Verify: Relevant Go/PostgreSQL/SDK/Node24/browser checks with executed evidence; final paired API/worker and local preview verification in M12.

### M07 — Trusted logging and operation replay

Depends: M06.

Own: `server/internal/journal/`, `contracts/graphql/schema.graphql`, `contracts/generated/`, `server/internal/agentapi/`, `server/internal/agentcontext/`, `server/internal/agentaccess/`, `contracts/mcp/`, `.artifacts/agents/M07/`, `docs/plan/tasks/M07.md`. Integrator owns graph and ledger.

Acceptance: Implement step 7 of `09-mcp-implementation.md`; record limitations honestly and preserve existing domain/security semantics.

Verify: Relevant Go/PostgreSQL/SDK/Node24/browser checks with executed evidence; final paired API/worker and local preview verification in M12.

### M08 — Reviewed agent actions

Depends: M07.

Own: `server/db/migrations/00014_agent_operations.sql`, `server/db/generated/`, `contracts/graphql/agents.graphql`, `contracts/operations/agents.graphql`, `contracts/generated/`, `server/internal/transport/graphql/gql/`, `server/internal/agentaccess/`, `server/internal/agentapi/`, `server/internal/transport/graphql/agents.go`, `web/src/app/features/settings/`, `.artifacts/agents/M08/`, `docs/plan/tasks/M08.md`. Integrator owns graph and ledger.

Acceptance: Implement step 8 of `09-mcp-implementation.md`; record limitations honestly and preserve existing domain/security semantics.

Verify: Relevant Go/PostgreSQL/SDK/Node24/browser checks with executed evidence; final paired API/worker and local preview verification in M12.

### M09 — Agent photo and receipt transfer

Depends: M08.

Own: `server/internal/transport/http/agent_media.go`, `server/internal/agentapi/`, `server/internal/media/`, `server/cmd/api/main.go`, `contracts/mcp/`, `.artifacts/agents/M09/`, `docs/plan/tasks/M09.md`. Integrator owns graph and ledger.

Acceptance: Implement step 9 of `09-mcp-implementation.md`; record limitations honestly and preserve existing domain/security semantics.

Verify: Relevant Go/PostgreSQL/SDK/Node24/browser checks with executed evidence; final paired API/worker and local preview verification in M12.

### M10 — Complete domain workflows

Depends: M09.

Own: `server/db/migrations/00015_agent_job_origins.sql`, `server/db/queries/agents.sql`, `server/db/generated/`, `server/internal/transport/http/agent_media.go`, `server/cmd/api/main.go`, `server/internal/agentapi/`, `server/internal/agentaccess/`, `server/internal/agentcontext/`, `server/internal/insights/`, `server/internal/knowledge/`, `server/internal/portability/`, `server/cmd/worker/main.go`, `contracts/mcp/`, `.artifacts/agents/M10/`, `docs/plan/tasks/M10.md`. Integrator owns graph and ledger.

Acceptance: Implement step 10 of `09-mcp-implementation.md`; record limitations honestly and preserve existing domain/security semantics.

Verify: Relevant Go/PostgreSQL/SDK/Node24/browser checks with executed evidence; final paired API/worker and local preview verification in M12.

### M11 — Client packages and onboarding

Depends: M10.

Own: `server/cmd/canopy-mcp/`, `examples/agents/`, `server/internal/transport/mcp/`, `.artifacts/agents/M11/`, `docs/plan/tasks/M11.md`. Integrator owns graph and ledger.

Acceptance: Implement step 11 of `09-mcp-implementation.md`; record limitations honestly and preserve existing domain/security semantics.

Verify: Relevant Go/PostgreSQL/SDK/Node24/browser checks with executed evidence; final paired API/worker and local preview verification in M12.

### M12 — Local deployment and interoperability

Depends: M11.

Own: `server/db/migrations/00015_agent_job_origins.sql`, `server/internal/insights/`, `server/internal/transport/graphql/journal_mutations_http_test.go`, `Makefile`, `server/internal/platform/config.go`, `server/internal/platform/config_test.go`, `server/cmd/api/main.go`, `server/internal/transport/http/agent_media.go`, `server/internal/agentcontext/`, `server/cmd/canopy-mcp/`, `server/internal/agentapi/`, `server/internal/agentaccess/`, `server/internal/transport/mcp/`, `server/internal/transport/graphql/agents_test.go`, `server/internal/portability/`, `web/src/app/features/settings/`, `examples/agents/`, `README.md`, `docs/plan/09-mcp-implementation.md`, `.artifacts/agents/M12/`, `docs/plan/tasks/M12.md`. Integrator owns graph and ledger.

Acceptance: Implement step 12 of `09-mcp-implementation.md`; record limitations honestly and preserve existing domain/security semantics.

Verify: Relevant Go/PostgreSQL/SDK/Node24/browser checks with executed evidence; final paired API/worker and local preview verification in M12.

### UI28 — Fullscreen photos and compact plant gallery

Depends: M12.

Own: `web/src/app/features/today/today.html`, `web/src/app/ui/photo.ts`, `web/src/app/ui/photo-viewer.ts`, `web/src/app/ui/photo-viewer.css`, `web/src/app/ui/photo-viewer.spec.ts`, `web/src/app/features/collection/plant-detail.ts`, `web/src/app/features/collection/plant-detail.html`, `web/src/app/features/collection/plant-detail.css`, `web/src/app/features/collection/compare-photos.ts`, `web/src/app/features/collection/compare-photos.spec.ts`, `web/src/app/features/journal/journal-timeline.html`, `.artifacts/agents/UI28/`, `docs/plan/tasks/UI28.md`. Integrator owns graph and ledger.

Acceptance: Fullscreen uncropped photos with original download, accessible dismissal and navigation; dedicated compact plant Gallery with complete pagination and two-photo comparison; preserve photo management and provenance. Publish tested frontend to local previews.

Verify: Node 24 targeted tests/build, desktop/phone Playwright and repository boundary/plan validation.

### UI29 — Quiet plant controls, filters and section navigation

Depends: UI28.

Own: `web/src/app/ui/tabs.ts`, `web/src/app/features/collection/plant-gallery.css`, `web/src/app/features/collection/collection.ts`, `web/src/app/features/collection/collection.html`, `web/src/app/features/collection/collection.css`, `web/src/app/features/collection/collection.spec.ts`, `web/src/app/features/collection/plant-detail.ts`, `web/src/app/features/collection/plant-detail.html`, `web/src/app/features/collection/plant-detail.css`, `web/src/app/features/collection/plant-form.ts`, `web/src/app/features/collection/plant-form.spec.ts`, `web/src/app/features/journal/journal-timeline.ts`, `web/src/app/features/journal/journal-timeline.html`, `web/src/app/features/journal/journal-timeline.css`, `web/src/app/features/journal/journal.spec.ts`, `web/src/app/ui/icon.ts`, `.artifacts/agents/UI29/`, `docs/plan/tasks/UI29.md`. Integrator owns graph and ledger.

Acceptance: Hide collection/Journey filtering in consistent modal forms with explicit Apply/Reset, preserve URL filters, add valid date bounds, quiet icon actions, archive in Edit with explicit confirmation and revision handling, clear responsive section navigation. Publish tested frontend locally.

Verify: Node 24 targeted tests/build, desktop/phone browser and accessibility checks; plan/import boundary validation.

### UI30 — Quiet journey headings and automatic quality-based covers

Depends: UI29.

Own: `web/src/app/features/capture/photo-queue.spec.ts`, `web/src/app/features/rounds/rounds.ts`, `web/src/app/features/rounds/rounds-actions.spec.ts`, `web/src/app/features/collection/plant-detail.html`, `web/src/app/features/collection/plant-detail.css`, `web/src/app/features/journal/journal-timeline.ts`, `web/src/app/features/journal/journal-timeline.html`, `web/src/app/features/journal/journal-timeline.css`, `web/src/app/features/journal/journal.spec.ts`, `web/src/app/features/capture/photo-queue.ts`, `web/src/app/features/rounds/rounds-actions.ts`, `server/db/migrations/00016_photo_cover_quality.sql`, `server/db/queries/photos.sql`, `server/db/queries/assets.sql`, `server/db/generated/`, `server/internal/media/`, `server/cmd/worker/main.go`, `server/internal/transport/graphql/automatic_cover_test.go`, `contracts/graphql/schema.graphql`, `contracts/generated/`, `server/internal/transport/graphql/gql/`, `.artifacts/agents/UI30/`, `docs/plan/tasks/UI30.md`, `docs/plan/tasks/UI30-contract-change.md`. Integrator owns graph and ledger.

Acceptance: Remove redundant gallery/story labels; align journey controls with first month; place Edit in plant header; worker scores sanitized thumbnails locally and attachment atomically chooses better current covers across every upload path, retaining explicit manual selection. Preserve capture dates, workspace boundaries, retries and existing photos. Publish matched API/worker and frontend locally with actual queue proof.

Verify: Node 24 tests/build and phone/desktop browser checks; Go/PostgreSQL quality, cover-selection and concurrency regressions; generation/plan/boundaries; paired runtime and queued worker proof.

### UI31 — Plant header quick actions and reliable history evidence

Depends: UI30.

Own: `web/src/app/features/journal/journal-timeline.ts`, `web/src/app/shell/shell.html`, `web/src/app/shell/shell.spec.ts`, `web/src/app/features/collection/plant-detail.ts`, `web/src/app/features/collection/plant-detail.html`, `web/src/app/features/collection/plant-detail.css`, `web/src/app/features/journal/quick-log.ts`, `web/src/app/features/journal/journal.spec.ts`, `server/internal/insights/`, `.artifacts/agents/UI31/`, `docs/plan/tasks/UI31.md`. Integrator owns graph and ledger.

Acceptance: Remove desktop primary Rounds destination while retaining mobile access; group photo/watering/edit actions in plant header using shared data-entry modals. Diagnose failed Meyer lemon synthesis, improve evidence prompting/repair without weakening validation, verify a real provider run. Preserve historical failure records, capture-date upload and manual watering semantics.

Verify: Angular affected tests/build, desktop/mobile Playwright, Go insights regressions, actual provider and queued-worker proof, matched local deployment, plan/boundary checks.

### UI32 — Readable plant suggestions and evidence disclosure

Depends: UI31.

Own: `web/src/app/features/insights/`, `.artifacts/agents/UI32/`, `docs/plan/tasks/UI32.md`. Integrator owns graph and ledger.

Acceptance: Action-first compact suggestion list with supporting context behind accessible disclosure; readable evidence counts instead of repeated generic chips, exact readable currency display and consistent accept/dismiss modals. Distinguish care from spending review and preserve all proposal/review/revision behavior. Publish locally.

Verify: Node 24 affected tests/build; phone/desktop light/dark Playwright including disclosure and modal mutations using fixtures; actual garden read-only smoke; plan/boundary checks.

### DEP01 — Container publishing and release promotion

Depends: UI32.

Own: `.github/workflows/`, `Dockerfile`, `.dockerignore`, `docker/Caddyfile.web`, `deploy/`, `scripts/release/`, `docs/deployment.md`, `README.md`, `.artifacts/agents/DEP01/`, `docs/plan/tasks/DEP01.md`. Integrator owns graph and ledger.

Acceptance: Docked-style tested dev publishing, versioned GHCR/GitHub releases and explicit digest-preserving latest promotion; matched API/worker server image, frontend TLS/proxy image and existing isolated media image, deployable Compose bundle, onboarding/upgrades documented. No registry push or public deployment during local validation.

Verify: Workflow lint and release metadata tests; actual image builds and isolated Compose smoke with migration/login/frontend/MCP and queued worker processing; compose configuration, plan/boundary gates.

### PUB01 — First GitHub repository and v1 release

Depends: DEP01.

Own: `web/src/app/features/today/today.spec.ts`, `server/internal/identity/password.go` (comment only), `.github/workflows/`, `.gitignore`, `scripts/release/`, `docs/deployment.md`, `.artifacts/agents/PUB01/`, `docs/plan/tasks/PUB01.md`. Integrator owns graph and ledger. Publishing uses an isolated clean source checkout; original local history and imported data remain local.

Acceptance: Create private dodgerbluee/canopy repository, publish credential/data-free source snapshot, tag v1.0.0 and verify GitHub release build. Investigate and fix workflow failures as needed; preserve live local garden.

Verify: Secret scan, reproducible generation, plan/boundary, build/test gates and actual GitHub Actions result/artifacts.

### DEP02 — Bundle existing local app into one deployment image

Depends: PUB01.

Own: `README.md`, `Dockerfile`, `docker/`, `deploy/`, `scripts/release/`, `.github/workflows/`, `docs/deployment.md`, `docs/plan/tasks/DEP02.md`, `.artifacts/agents/DEP02/`. Integrator owns graph and ledger.

Acceptance: Package the existing frontend/API/worker/migration/codec tools in one application image, with a two-service Compose using PostgreSQL. Preserve current local app behavior and existing Docker-based codec isolation. No application/authentication/kernel sandbox redesign. Publish and verify the bundled image for Portainer.

Verify: Actual bundled startup/migrations/login/queued photo/receipt processing; supervised child failure/shutdown; Compose validation; release workflow lint and publication.

### AUTH01 — First-run registration, users and OIDC

Depends: DEP02.

Own: `server/internal/identity/`, `server/internal/transport/http/`, `server/internal/transport/graphql/`, `server/internal/platform/`, `server/cmd/api/main.go`, `server/db/migrations/00017_users_oidc.sql`, `server/db/queries/sessions.sql`, `server/db/queries/owner.sql`, `server/internal/portability/`, `server/db/generated/` (generator only), `server/go.mod`, `server/go.sum`, `web/src/app/core/auth/`, `web/src/app/features/auth/`, `web/src/app/features/settings/`, `web/src/app/app.routes.ts`, `web/e2e/`, `web/playwright.accounts.config.ts`, `docs/deployment.md`, `docs/plan/03-contracts.md`, `docs/plan/tasks/AUTH01.md`, `.artifacts/agents/AUTH01/`. Integrator owns graph and ledger.

Acceptance: Browser first-run setup creates one administrator atomically; preserve migrated owner and garden. Admins manage shared-garden users (create, role, disable, password reset) without disabling/demoting the final active administrator. Generic OIDC settings support discovery, encrypted client secret, connection test and explicit account linking; optional new SSO users receive member role only. State, nonce, PKCE, issuer/audience/signature/expiry and one-time callback binding enforced. Disabled users lose sessions. Members cannot manage authentication, AI/provider credentials or agent access. Existing local recovery remains available. Consistent modal settings forms.

Verify: Disposable PostgreSQL integration including setup race, migration compatibility, role/session boundaries, signed local OIDC provider with negative callbacks; Angular tests/build and browser walkthrough. No live data replacement or remote deployment implied.

### AUTH02 — First-run setup spacing refinement

Depends: AUTH01.

Own: `web/src/app/features/auth/setup.html`, `web/src/app/features/auth/setup.ts`, `web/src/app/features/auth/setup.css`, `web/src/app/features/auth/login.css`, `docs/plan/tasks/AUTH02.md`, `.artifacts/agents/AUTH02/`. Integrator owns graph and ledger.

Acceptance: Consistent label/input/help spacing and intentional header, introduction, section and footer spacing on desktop/mobile. Preserve form behavior and preview garden data. Refresh local Docker preview.

Verify: Production image build; read-only Playwright layout/accessibility/screenshots across desktop/mobile widths.

### AUTH03 — Browser theme defaults across entry routes

Depends: AUTH02.

Own: `web/src/app/ui/theme.ts`, `web/src/app/ui/theme.spec.ts`, `web/src/app/app.config.ts`, `web/src/index.html`, `web/src/app/features/auth/setup.css`, `web/src/app/features/auth/setup.html`, `web/src/app/features/auth/login.css`, `web/src/app/features/auth/login.html`, `docs/plan/tasks/AUTH03.md`, `.artifacts/agents/AUTH03/`. Integrator owns graph and ledger.

Acceptance: Browser color preference is the default on setup, login and app routes, including first paint and live preference changes. Explicit saved preferences remain respected; no default is persisted. Verify served spacing and refresh the Docker preview without changing garden data.

Verify: Theme regression tests, production build and real-browser light/dark/system-change/override checks, plus entry-page layout inspection.

### AUTH04 — Modern single-column registration

Depends: AUTH03.

Own: `web/src/app/features/auth/setup.html`, `web/src/app/features/auth/setup.css`, `web/src/app/features/auth/setup.ts`, `web/src/app/features/auth/setup.spec.ts`, `web/e2e/accounts.spec.ts`, `docs/plan/tasks/AUTH04.md`, `.artifacts/agents/AUTH04/`. Integrator owns graph and ledger.

Acceptance: Traditional modern registration card with one column of account fields. Garden preferences use existing valid defaults and remain available in a collapsed section. Retain browser theme behavior and existing setup API. Serve locally without resetting records.

Verify: Focused setup tests, production image build and read-only desktop/mobile/browser-theme/accessibility checks; preserve registration submission behavior.

### AUTH05 — Registration name and password policy

Depends: AUTH04.

Own: `web/src/app/features/auth/setup.html`, `web/src/app/features/auth/setup.ts`, `web/src/app/features/auth/setup.spec.ts`, `web/src/app/core/auth/accounts.ts`, `web/src/app/core/auth/password.ts`, `web/src/app/features/settings/accounts.ts`, `web/src/app/features/settings/accounts.html`, `web/e2e/accounts.spec.ts`, `server/internal/identity/users.go`, `server/internal/identity/users_test.go`, `docs/deployment.md`, `docs/plan/tasks/AUTH05.md`, `.artifacts/agents/AUTH05/`. Integrator owns graph and ledger.

Acceptance: Remove Your name from registration; backend defaults display name to username when absent. Ten-character minimum for browser-created/changed account passwords, consistent across frontend and backend. Retain existing credentials and serve updated local Docker preview without resetting data.

Verify: Backend password boundary and setup tests; Angular tests/build; browser form inspection and actual queued-job compatibility on the rebuilt API/worker image.

### MIG01 — Local production migration and registration handover

Depends: AUTH05.

Own: `server/internal/identity/`, `server/internal/transport/http/`, `server/cmd/admin/main.go`, `server/db/migrations/00018_registration_handover.sql`, `server/db/queries/owner.sql`, `server/db/generated/` (generator only), `web/src/app/features/auth/`, `web/src/app/core/auth/accounts.ts`, `web/src/app/app.routes.ts`, `docs/deployment.md`, `docs/plan/tasks/MIG01.md`, `.artifacts/agents/MIG01/`, `.artifacts/dev/migration/`. Integrator owns graph/ledger. Operator scope: privately back up the current 4311 database/storage/config, stop its old API/worker/UI after verification, deploy combined app with a dedicated PostgreSQL instance at the same local ports, preserve rollback assets and the original checkout/data. No source changes to the original checkout.

Acceptance: One-time, expiring operator-prepared registration handover keeps the existing garden owner/workspace/entity IDs and settings, replaces primary credentials with user-chosen registration and revokes old sessions. No unauthenticated open claim. Migration preserves all garden records, original media and encryption keys/provider configuration. User completes registration; agent does not choose their production password. Preserve rollback and verify actual worker processing.

Verify: Handover/token/concurrency/recovery tests, frontend tests/build, restored data and file manifests, counts/IDs/financial totals, private media, AI connection decryption, queued-job completion and actual local browser registration availability.

### PUB02 — Publish registration/OIDC v1.1.0

Depends: MIG01.

Own: `.gitignore`, `.dockerignore`, `deploy/compose.yaml`, `scripts/release/`, `.github/workflows/`, `docs/deployment.md`, `docs/plan/tasks/PUB02.md`, `docs/plan/tasks/MIG01.md`, `.artifacts/agents/PUB02/`, `.artifacts/agents/MIG01/EVIDENCE.md`. Integrator owns graph/ledger. Release integration includes the already accepted AUTH01–AUTH05/MIG01 source changes and bounded fixes required by actual release checks. No production data, configuration, credentials or original development Git history may be published.

Acceptance: Verify owner registration complete and existing garden retained. Push credential/data-free source to dodgerbluee/canopy, publish stable v1.1.0 combined AMD64/ARM64 image, verify release assets and promote exactly that digest to latest. Preserve the running production garden.

Verify: Known-private-value scan, plan/boundary/generation/release-tool checks, full GitHub CI and image smoke, published manifest/checksums, latest promotion digest equality.

### DEP03 — Socket-free bundled processing and v1.1.2

Depends: PUB02.

Own: `server/internal/media/processor/`, `server/internal/insights/receipt_process_test.go`, `server/internal/platform/`, `server/cmd/worker/main.go`, `Dockerfile`, `docker/canopy-run`, `deploy/compose.yaml`, `docs/deployment.md`, `scripts/release/`, `scripts/e2e_with_worker.sh`, `.github/workflows/`, `docs/plan/tasks/DEP03.md`, `.artifacts/agents/DEP03/`. Integrator owns graph/ledger.

Acceptance: Default app + PostgreSQL deployment has no Docker socket, host work directory or runtime Docker CLI requirement. Run installed codecs/OCR as bounded unprivileged subprocesses; preserve formats, orientation, capture dates, derivatives and receipt extraction. Context cancellation kills codec process groups. Publish tested v1.1.2 and promote its verified digest to latest. Preserve local production data/configuration.

Verify: Runner tests for command restrictions, path mapping, bounded output, missing tools, cancellation and resource limits; real HEIC/JPEG/WebP/PNG and PDF/image OCR in the combined image without socket mounts; actual queued photo job, capture date and restart smoke; release CI and AMD64/ARM64 manifests.

### MIG02 — Remote production data migration

Depends: MIG01, DEP03.

Own: `docs/plan/tasks/MIG02.md`, `.artifacts/agents/MIG02/`, `.artifacts/dev/migration/`, `docs/deployment.md`. Integrator owns graph/ledger. Authorized operator scope: SSH to the user-authorized destination using the supplied identity; inspect, back up and migrate into containers canopy/canopy-db. Privately preserve remote configuration and existing data before replacement. Keep local source and rollback assets.

Acceptance: Transfer complete database and media/encryption-key storage consistently; retain garden IDs, users/passwords, acquisitions, care, knowledge, AI and agent records. Preserve target connection/public URL settings and verify counts, row fingerprints, file hashes, readiness and worker processing. Do not overwrite an unrelated garden without clarification. Do not disclose credentials or publish private backups.

Verify: Source/destination inventories and backups with checksums, quiesced final snapshot, restored table and file reconciliation, actual target readiness/media and worker verification. Keep both source and destination rollback backups.
