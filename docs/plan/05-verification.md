# Verification, quality gates, and operations

## Testing architecture

Tests prove observable invariants, not incidental method calls. Fakes only at genuine time/ID/provider/storage boundaries; database behavior uses PostgreSQL. No mocked SQL for constraints, transactions, pagination, or job atomicity. Standard Go test/table tests/fuzzing; Angular CLI Vitest + TestBed/testing-library patterns; Playwright browser journeys. Avoid sleeps: fake clock/scheduler, deterministic fixtures, wait for actual conditions.

| Layer | Required evidence |
|---|---|
| Pure rules | Money allocations/rounding, recurrence windows/DST/leap years, unit conversion, care context and missing inputs, insight validation. Table and property/fuzz tests. |
| Use cases | In-memory ports for provider-independent behavior; revision conflict/idempotency/acceptance policy. Verify outcome, no tautological mock call assertions. |
| Postgres | Apply migrations into isolated test DB; real constraints, workspace isolation, tx rollback, concurrent writes, keyset paging and query counts. Each test namespace/database isolated; cleanup deterministic. |
| API | httptest full GraphQL+HTTP adapters against test DB; auth/CSRF/limits/typed errors and real generated operation documents. No resolver-only evidence for API behavior. |
| Worker | River test helpers plus real tx integration; crash/retry/late result; duplicate logical work; provider test servers, corrupt media and bounded subprocess execution. |
| Angular | Component interactions and route/store states with generated typed fixtures; HttpTestingController for transport; signals settle using Angular notifications, not forced detectChanges hiding missing updates. |
| Browser | Real frontend + backend + DB + worker, fake external HTTP providers only. Request/response wiring must be real. |
| AI eval | Versioned inputs and rubric, deterministic shape/evidence tests plus separately run real-provider pilot; probabilistic narrative not snapshot-tested word for word. |

## Test corpus and acceptance scenarios

Synthetic deterministic fixture: 7 records (five individual trees, two berry groups), 3 locations, 2 currencies, unknown prices/cultivars, 100 photos, one shared receipt, a year of care activities, archived plant, expired suggestion. Fixture builders inject fixed clock and UUID sequence, never depend on user's real plant photos. Legally usable fixture images include HEIC/rotated/GPS-tagged JPEG/WebP, truncated, oversized-header, and mislabeled files. Preserve fixture attribution.

Essential suites, referenced by task IDs:

- **AUTH:** bootstrap no default password; login errors generic; session rotation/expiry/revocation; CSRF, hostile Origin; unauthenticated media/export denied. Cross-workspace fixtures check every link, even though v1 has one owner.
- **JOURNAL:** create/backdate/correct/void, same-time stable paging, filters/cursor mismatch, archive retains history, unknown capture date explicit.
- **MONEY:** multi-plant one receipt, proportional largest-remainder rounding, zero-subtotal handling, exact reconciliation, currencies grouped, revision race and rollback; no duplicated expense in totals.
- **MEDIA:** valid phone HEIC orientation, GPS removal on derivatives, failed/truncated/duplicate upload, reservation expiry, unauthorized download, delete while processing, orphan sweep, quota enforcement, time/memory limits.
- **CARE:** spring/fall Chicago DST, southern-hemisphere explicit seasons, Feb29, New-Year window, completion-relative vs calendar cadence, snooze/skip/backdated completion, duplicate scheduler tick, outage catch-up, covered pots ignore rain, missing cultivar blocks sensitive advice.
- **AI:** no provider, text-only provider refusing vision, timeout/429/schema failure, injected receipt instructions, fabricated citation, stale photo/context, cancelled target, duplicate request, bounded input/cost, privacy opt-in, uncertain findings, accept proposal exactly once.
- **PORTABILITY:** purge→original-create replay returns RESOURCE_GONE, purge while export/analysis is in flight, inspect copied data/redacted audit/idempotency, restore deletion ledger before reopening; export all originals and metadata, archive path traversal checks, manifest checksums, restore on blank host, expired/private downloads; hash and financial/care record reconciliation.

Minimum real-provider evaluation pack: 30 legally usable cases: healthy/ambiguous/damaged images, before/after pairs with lighting/viewpoint changes, citrus/fig/berries, receipt tables, missing context and adversarial document text. Human-reviewed labels from reliable sources where available; do not label uncertain pathology as truth. Required pass: 100% schema and evidence-reference integrity, zero autonomous action or fabricated evidence accepted by validator, >=90% reviewer-rated useful follow-up checks, abstention on all deliberately underdetermined high-impact cases. Confidence is not diagnostic accuracy. Small sample limitation documented. Failure disables that capability behind flag while core journal remains usable; R3 is not marked complete until evaluated actual configured provider meets gate.

## CI and local commands to implement

Commands below are target contract, not already available commands. T03 makes them real and tests a fresh checkout.

| Command | Behavior |
|---|---|
| make bootstrap | Verify pinned tools; install locked deps; no production secrets/network provider needed. |
| make dev | Start local DB/API/worker/web; bounded health waits; print safe local URL. |
| make generate | gqlgen + sqlc + GraphQL TS documents; deterministic. |
| make check | format, Go vet/lint, Angular lint/typecheck, boundaries, generated diff, contract validation. |
| make test-unit | no containers or internet; Go pure/use-case tests + Angular unit tests. |
| make test-integration | isolated PostgreSQL + media temp dirs; includes API/queue/media tests. |
| make test-e2e | reproducible full stack with fake provider fixtures, seed only test DB. |
| make test-visual | fixed screenshot suite, produces reviewable diffs. |
| make test-perf | seeded workload and browser budgets; outputs environment and percentile report. |
| make test-ai-live | explicit opt-in configured provider, bounded test budget, no private user files. |
| make verify | check + unit + integration + e2e + accessibility + build + dependency/security checks. |
| make backup / make restore-test | exercise production-shaped encrypted archive and clean restore; never default to live destination. |

CI fast lane on each change: changed package lint/types/unit/contract generation; required full integration and critical e2e before merge. Full visual/perf/restore/AI capability evaluation at release and relevant changes. Generated diff check uses temp regeneration/clean diff rather than accidentally swallowing unrelated user edits. Go race test covers concurrent package paths and worker integration. No forced global 100% coverage; enforce meaningful critical invariant coverage. Suggested pure care/money/validation branch coverage >=90%; exclude generated code from coverage. No skipped tests, weakened assertions, or approved screenshots authored solely to satisfy failure.

## Performance targets and measurement

Reference single-host budget: 4 CPU cores, 8 GiB RAM, local SSD; report actual machine. Dataset: 1,000 plants, 20,000 activities, 10,000 photo metadata rows with thumbnails, 1,000 purchases. Local model runtime lives outside this budget. 10 concurrent browsing sessions in load test even though owner-only v1, to expose query amplification.

- Core API reads p95 <200ms, writes <350ms excluding uploads and provider work; <=1% errors and no ownership leaks in 10-minute local load run. Media originals are separate from metadata timing.
- Initial app JS <=300 KiB gzip, lazy feature chunks <=200 KiB each (report codegen overhead); below-fold comparison/insights code lazy loaded. LCP <=2.5s, CLS <=0.1 on fixed mobile profile. Fixed environment/browser/network documented; lab INP proxy interaction <=200ms, field INP only after real usage.
- Thumbnails responsive AVIF/WebP/JPEG according to proven codec support, declared dimensions, lazy loading; no full-resolution photo grid. Virtualization only if actual measured timeline requires it; start paging.
- Upload acknowledgement streams, bounded memory; 12MP image thumbnail pipeline p95 <10s on reference worker, HEIC separately measured. Main navigation never waits on AI. Async job state visible within 2s polling window.
- Query count <=8 for collection of 50 including covers/next task; bounded rows/page and query plans indexed. Run EXPLAIN on key workloads.

## Security and data handling checks

Threat model covers exposed reverse proxy, malicious attachment/provider output, session theft/CSRF, cross-workspace IDs, path traversal, decompression bombs, prompt injection, logs/backups. Dependency audit tools pinned; Go vulncheck, npm audit with reviewed findings, image scanner at build. Findings tracked with severity and exploitability; no silent allow-all. No external telemetry contains photos, receipt text, coordinates, prompts, tokens, or passwords. Logs use IDs and bounded error codes.

Asset processing runs nonroot, no network, read-only container root, constrained temp dir, subprocess timeout (30s) and memory/CPU/pixel limits. Need actual tested process/container enforcement, not just an intention to limit image size. API and worker share only required media volume; no Docker socket. Production only reverse proxy exposed; DB/internal APIs private. Profile sources and external links cannot request arbitrary internal URLs. Provider result content never executes code.

## Operational runbooks

Configuration validated at startup with safe diagnostics. `.env.example` contains names/placeholders only; real keys mounted as secrets or environment. Owner selects runtime providers/retention; no real secrets in git/agent prompts.

Migrations: forward SQL with explicit failure stop, advisory lock, fresh-install and previous-release upgrade tests. Release uses expand/contract where needed; app rollback only if schema remains compatible. No automatic down migration on valuable data. Pre-upgrade backup mandatory in runbook, not unrequested external action now. Worker drains on shutdown (bounded 30s), unfinished jobs retry safely. Startup migrations executed once by migration command before API/worker readiness.

Backup default: nightly encrypted archive, retain 7 daily + 4 weekly, operator supplies encryption key outside archive. Target RPO <=24h and RTO <=60min at reference dataset. Implementation initially favors consistency over zero downtime: maintenance mode blocks writes/uploads, pauses scheduler/worker claims and drains jobs; acquire consistent DB dump plus referenced immutable originals/derivatives and manifest; resume even on failure using finally/defer; encrypt and checksum. Verify manifest, then rotate only after success. Operator chooses off-host destination; local backup alone does not protect host loss, surfaced in setup/runbook.

Restore into empty volumes/database, validate archive paths/hashes, restore schema/data/media and River schema, apply latest separately retained encrypted deletion ledger before opening access, invalidate sessions, reset queued RUNNING work to retry-safe state, verify counts/hash samples/purchase totals/care occurrence uniqueness, bootstrap recovery login, run smoke journeys. Never apply archive over active database automatically. Release gate measures elapsed restore time and records proof. User export is JSON+CSV+files+schemaVersion/provenance manifest, consistent snapshot (maintenance export initially), excludes secrets, sessions, idempotency receipts and raw private operational logs. Export TTL 24h, authenticated only. General arbitrary import is deferred; tested restore is the recovery path.

Health: /health/live for process, /health/ready DB/media/schema; worker heartbeat age and oldest queued job; disk space, failed jobs, provider budget and snapshot age in owner settings. No secrets in health output. Alerts: disk <10%, queue oldest >15min excluding deliberately scheduled jobs, worker stale >2min, backup >26h. Notifications and backup failures visible in UI even without email configured.

## Release checklist evidence

Fresh install, upgrade, auth, all four user requirements, no-provider mode, real configured AI pilot, browser keyboard/contrast/zoom, dark/light mobile screenshots, performance report, queue/provider failure injection, backup/restore, export checksum verification, licenses/SBOM, no known unmitigated critical/high exploitable issue. Reviewer records commit, commands, results, deviations, and outstanding risks. Feature flags default off for unevaluated AI features. Documentation never describes a disabled/mock capability as implemented.
