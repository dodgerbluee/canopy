# ADR-0001: Toolchain and local-model compatibility (T00)

Date: 2026-11-01. Status: ACCEPTED by integrator (see §9).
Execution workspace: `/private/tmp/canopy-t00-20261101-a7f3/` (disposable).
Evidence: `.artifacts/agents/T00/` (baseline manifest, per-step logs, versions files).
Baseline SHA256 manifest of planning artifacts recorded before execution; re-verified byte-identical after execution (`MANIFEST-UNCHANGED`).

## 1. Exact versions, image digests, licenses

Host toolchain (observed, not installed by T00):
- go1.26.0 darwin/arm64; Node v25.2.1 (UNSUPPORTED for Angular 22 — see §3); npm 11.6.2; Docker CLI 29.0.1 / server 29.0.1 (Docker Desktop, aarch64); opencode v2.0.29.

Frontend (resolved from npm registry, exercised in container):
| Package | Version | License |
|---|---|---|
| @angular/core | 22.2.4 | MIT |
| @angular/cli / @angular/build | 22.2.5 | MIT |
| @angular/cdk | 22.2.5 | MIT |
| typescript | 6.0.3 (range >=6.0.0 <6.1.0) | Apache-2.0 |
| rxjs | 7.8.2 | Apache-2.0 |
| vitest (via @angular/build:unit-test) | 5.0.5 | MIT |
| graphql | 17.0.2 | MIT |
| @graphql-codegen/cli | 7.4.6 | MIT |
| @graphql-codegen/typescript | 6.1.2 | MIT |
| @graphql-codegen/typescript-operations | 6.2.0 | MIT |
| @graphql-codegen/typed-document-node | 7.2.0 | MIT |
| @graphql-typed-document-node/core | 3.2.0 | MIT |

Backend (go.mod direct deps; go.sum sha256 `8730d6aa199548f8e476992753a9842c78a0f4ebd7c31554b6ce3667cffa3d5c`):
| Module | Version | License |
|---|---|---|
| github.com/99designs/gqlgen | v0.17.98 | MIT (upstream repo; module zip has no LICENSE file — flagged) |
| github.com/vektah/gqlparser/v2 | v2.5.64 | MIT |
| github.com/jackc/pgx/v5 | v5.12.0 | MIT |
| github.com/riverqueue/river (+riverpgxv5) | v0.51.0 | MPL-2.0 |
| github.com/sqlc-dev/sqlc (go.mod `tool` pin) | v1.31.1 | MIT |

Container images (pinned by digest):
- docker.io/library/node:24 @ `sha256:e7add2a490bb17d97b485b8641bd88455fff7839ade0f5eba493c3508124d413` (linux/arm64; Node 24.x, npm 11.19.1)
- docker.io/library/postgres:17 @ `sha256:3b0cda93be7c8dba2ed484d19393d93845dc3dafb88973e248ba8e6d99d784e1` (linux/arm64)
- docker.io/library/ubuntu:24.04 @ `sha256:fe81560990fae86dc4743202b2cb4d3df98d0bcf949173cecef15ce1127ef852` (linux/arm64)
- spike media image `canopy-t00-media:spike` @ `sha256:3cad90e82761cf27f6ab328f0facd3d67867ac6f70abc257cc87f9557fec2912` (built from media/Dockerfile in workspace)

Media runtime packages (Ubuntu 24.04): libvips 8.15.1-1.1build4 (LGPL-2.1+); libheif1 1.17.6-1ubuntu4.9 (MIT) with plugin packages; libde265 1.0.15 (LGPL-3.0); x265 (GPL-2.0+, encoder — spike fixture creation only, NOT needed to decode); tesseract 5.3.4-1build5 (Apache-2.0); poppler 24.02.0-1ubuntu9.11 (GPL-2.0+/MIT dual).

## 2. Commands, exit codes, evidence paths

All commands and sanitized outputs: `.artifacts/agents/T00/step1-versions.txt`, `step2-angular.md`, `step3-go.md`, `step3-go-versions.txt`, `step4-media.md`, `step4-media-versions.txt`, `step4-media-pipeline.txt`, `step4-limits.txt`, `step5-model.md`. Key commands (full verbatim in evidence):
- `npx -y @angular/cli@22.2.5 new web-spike --style=css --skip-git --zoneless --ssr=false --defaults` → 0 (in node:24 container)
- `npm test` → 0 (4/4 tests incl. graphql typing spec); `npm run build` → 0 (217.09 kB initial)
- `go run github.com/99designs/gqlgen generate` → 0; `go tool sqlc generate` → 0; `go vet ./...` → 0
- `go test ./graph/...` → ok; `PGTEST_DSN=... go test -run TestDomainRowPlusRiverInsertTxAtomicity -v .` → PASS
- `bash media/pipeline.sh` → 0; `bash media/limits.sh` under restricted container → recorded exits
- `opencode run --standalone --model 'strata/flash-next#fast|#deep' --format json --file <packet> ...` → 0 (×2), SIGINT test recorded

## 3. Angular build/test + typed GraphQL — PASS (with dev-environment requirement)

- Angular 22.0.x requires Node `^22.22.3 || ^24.15.0 || ^26.0.0`, TS `>=6.0.0 <6.1.0`, RxJS `^6.5.3 || ^7.4.0` (angular.dev/reference/versions, fetched 2026-11-01). Host Node 25.2.1 is NOT supported → spike ran in pinned node:24 container; all checks passed there.
- Zoneless is the v22 default (no zone provider/zone.js in generated project). Signal component test (signal + computed + input signal, DOM update after click) passes under Vitest 5 + jsdom.
- Typed GraphQL: codegen plugins `typescript` + `typescript-operations` + `typed-document-node` (all three required — without typescript-operations the generated document fails to compile). HttpClient wrapper accepting `TypedDocumentNode<Data,Variables>` compiles; `@ts-expect-error` on a nonexistent result field is consumed → result types are not `any`. Strict TS confirmed in generated tsconfig.
- CDK 22.2.5 installs and builds cleanly with core 22.2.4.

## 4. Go + PostgreSQL + sqlc + gqlgen + River — PASS

- gqlgen v0.17.98 schema-first generation + resolver + query executed over `handler.NewDefaultServer` via httptest (no `graphql.Do`/`Eval` convenience API in this version — tests must go through HTTP transport).
- sqlc v1.31.1: config key is `schema:` (not `migrations:`); pgx/v5 codegen; pinned via go.mod `tool` directive (no global install).
- River v0.51.0: requires `Workers` when `Queues` set; no `river.WorkerFunc` helper (embed `river.WorkerDefaults[T]`).
- Atomicity proven on postgres:17 container: sqlc-typed `InsertPlant` + `client.InsertTx` in one pgx tx → ROLLBACK leaves 0 plants + 0 `river_job` rows; COMMIT leaves 1 + 1.

## 5. HEIC/image processing, OCR, resource isolation — PASS (with explicit mechanisms)

- HEIC decode works in vips 8.15.1 ONLY when `libheif-plugin-libde265` is installed (Ubuntu 24.04 splits libheif into plugins; `libheif-plugins-all` does not exist there).
- EXIF Orientation=6 JPEG → thumbnail output transposed 1200x1600→400x300: orientation applied.
- GPS/metadata is NOT stripped automatically by `vips thumbnail` in 8.15; explicit `--keep none` on save (e.g. `vips jpegsave in out --keep none`) yields 0 GPS tags in derivative.
- Tesseract 5.3.4 + Poppler 24.02: hand-authored PDF → `pdftoppm -png -r 150` → OCR exact match (`TOTAL12.34USD`). Synthetic image OCR matched `TOTAL` but misread `12.34`→`127.34` (fixture noise, not a quality benchmark).
- Resource isolation effective at CONTAINER level: `--network=none` blocks connect() (subprocess inherits only because it runs inside the container — no automatic subprocess inheritance); `timeout` exit 124; `--pids-limit` fork refusal; `--memory=256m --memory-swap=256m` OOM-kills a 500MB allocation (exit 137). Without `--memory-swap` pinned, allocation survived (swap accounting).
- vips streaming/mmap ops can SURVIVE container memory limits via page-cache paging → pixel budgets must be enforced in application code by reading headers before decode (T14 requirement), with container limits as backstop.

## 6. Local-model tool-use and permission enforcement — PASS

- `strata/flash-next#fast`: bounded edit+test task in 29.6s wall; `#deep`: 50.3s. Both produced correct fixes, meaningful table-driven tests, and passing `go test` (independently re-verified by integrator against repo state, not model claims).
- Permission enforcement is real and path-granular via project `opencode.json`: out-of-scope `write` → `Permission denied: edit` (file never created, no bypass attempted); non-allowlisted `shell` → `Permission denied: shell` (model adapted to allowed tools). No `--auto` used.
- Interruption: SIGINT mid-run exits cleanly, no orphan processes, no partial writes at that point.
- JSON `--format json` parseable end-to-end. Tokens (step-finish): fast in 3100/out 978, deep in 3019/out 890. First-token latency NOT exposed by CLI — unmeasured (limitation, not a pass claim). Reasoning tokens reported 0 for both variants.
- Two sequential runs stable on the single endpoint; no evidence supports >1 concurrent worker. Keep one active worker (06-delegation unchanged).

## 7. Proposed architecture changes (proposals — integrator decision, not applied to 02-architecture.md)

1. **Dev/runtime Node line**: pin Node 24 LTS (>=24.15.0) for host dev and CI; host currently runs unsupported Node 25.2.1. (Fact: incompatibility; proposal: adopt Node 24.)
2. **Media runtime image**: must install `libheif-plugin-libde265` explicitly; document `--keep none` metadata stripping as mandatory in T14 design.
3. **Pixel budget**: add explicit max-pixels header check to T14 (container memory limits alone insufficient for mmap-streaming vips).
4. **Docker memory flags**: worker container must set `--memory-swap` equal to `--memory` (or cgroup v2 equivalent) for the limit to be real.
5. **sqlc config**: use `schema:` key (v1.31 syntax) in T03 codegen config.
6. **gqlgen testing**: integration tests go through `handler.NewDefaultServer` over HTTP; no direct exec API.
7. **HEVC patents**: libde265/libx265 are free-licensed but HEVC patent royalties may apply to commercial redistribution; acceptable for self-hosted single-owner deployment — integrator to acknowledge.
8. **River license**: MPL-2.0 (file-level copyleft) — compatible with the plan; record in dependency inventory.

## 8. Blockers and review readiness

- No unresolved blockers for T01–T03: every stack decision has an exact pinned version and a reproduced command set.
- Environment prerequisites observed at execution time (not blockers, but required for reruns): Docker Desktop daemon must be running (it was started during T00 with `open -a Docker`; the disposable postgres container was removed afterwards).
- Host Node 25.2.1 remains unsupported for Angular work — either install Node 24 LTS or standardize on the pinned container; decision requested from integrator (proposal §7.1).
- Unmeasured: first-token latency (CLI limitation); model throughput under concurrent load (not tested; one-worker default retained).
- T00 is READY FOR INDEPENDENT REVIEW. Author does not mark ACCEPTED. Independent reviewer should re-run: `go test` in go-spike, `npm test`/`npm run build` in web-spike (node:24 container), `media/pipeline.sh`, and one `opencode run` benchmark, against the digests above.

## 9. Integrator review and acceptance (2026-11-01)

Independent re-run from the integrator session (separate from authoring session):

- `go test ./...` in go-spike: gqlgen + sqlc packages pass; River atomicity test passed with `PGTEST_DSN` against a fresh disposable `postgres:17` (digest `3b0cda93…`) container.
- `npm test` + `npm run build` in web-spike under pinned `node:24` (digest `e7add2a4…`): 4/4 tests pass, bundle builds.
- Media spike image probe: `vips-8.15.1`, `tesseract 5.3.4` present in `canopy-t00-media:spike`.

Integrator decisions on §7 proposals and §8 open items:

1. **Node line**: adopt Node 24 LTS via the pinned `node:24` container for ALL web tooling (dev, codegen, CI). Host Node 25.2.1 is not used for project work; no global installs. All `make` web targets run through Docker.
2. **HEVC/HEIC**: acknowledged. Decoding uses `libheif-plugin-libde265`/HEVC in the pinned media image. This project is self-hosted single-owner software; we do not distribute encoders. Patent exposure accepted as an operator-run dependency; media image is pinned by digest and documented in the license inventory.
3. **River license**: River OSS is MPL-2.0; used unmodified as a library. Recorded in the license inventory (`.artifacts/agents/T00/` and CI scan). No Pro features.
4. Container-only dev is the standard development mode; Docker daemon must be running for web/DB targets.

Status: ACCEPTED. Downstream tasks T01.r1/T02.r1/T03 may dispatch against the pinned versions and digests in §2–§5.
