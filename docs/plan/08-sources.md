# Sources, assumptions, and decision log

Verified by reading local files/CLI and primary project documentation on 2026-11-01. Exact patch versions must be frozen by T00; planning date does not authorize future unreviewed dependency upgrades.

## Primary engineering references

- Angular supported release policy: https://angular.dev/reference/releases
- Angular/Node/TypeScript/RxJS compatibility: https://angular.dev/reference/versions — documentation lists Angular 22.0.x compatibility; choose supported stable patch after dependency spike.
- Zoneless Angular: https://angular.dev/guide/zoneless — stable signals-driven notification model; no ZoneJS required for selected line.
- Angular testing: https://angular.dev/guide/testing — CLI uses Vitest for new projects.
- gqlgen query complexity: https://gqlgen.com/reference/complexity/ — field cost must account for requested page sizes; fixed limit alone without appropriate weights is insufficient.
- gqlgen dataloaders: https://gqlgen.com/reference/dataloaders/ — request-scoped batch reads avoid resolver N+1.
- sqlc transactions: https://docs.sqlc.dev/en/latest/howto/transactions.html — transaction-bound typed queries using pgx, no custom ORM required.
- River transactional enqueue: https://riverqueue.com/docs/transactional-enqueueing — application state and durable job insertion share a PostgreSQL transaction. Use OSS capabilities only; application cursor handles scheduler catch-up.
- TypedDocumentNode generation: https://the-guild.dev/graphql/codegen/plugins/typescript/typed-document-node — generated variables/results link frontend operations to schema.
- OpenCode v2 provider configuration: https://opencode.ai/v2/docs/providers — provider/model alias differs from provider modelID. Installed CLI help is authoritative for invocation in this local environment.

## Plant ecosystem references

- HortusFox management: https://hortusfox.github.io/plants.html
- HortusFox API: https://hortusfox.github.io/api.html
- farmOS data model: https://farmos.org/model/type/log/
- Pl@ntNet identification API: https://my.plantnet.org/ and https://my.plantnet.org/doc/getting-started/faq
- Perenual botanical/care API: https://perenual.com/docs/api — possible supplemental source, not v1 dependency or region-specific schedule authority.
- Kindwise hosted health capabilities: https://www.kindwise.com/faq — optional future provider, proprietary hosted API is not claimed open source.
- Open-Meteo weather fields: https://open-meteo.com/en/docs — fresh/stale policy is our product decision, not a guarantee from provider.
- UMN indoor citrus guidance: https://extension.umn.edu/garden-and-home/yard-and-garden/gardening-in-minnesota/growing-citrus-indoors
- Blackberry cane/pruning distinctions: https://content.ces.ncsu.edu/blackberry-pruning-a-practical-guide
- OpenFarm shutdown: https://github.com/openfarmcc/OpenFarm — avoid dead API dependency.

These plant references motivate source-backed profiles; they do not establish the owner's actual region or prescribe a universal schedule. T21 researches profile-specific region/cultivar guidance and documents reuse permissions rather than copying whole pages.

## Local evidence

- Read relevant provider/model/capability fields from local OpenCode config with apiKey redacted; no credentials copied to repository.
- Installed OpenCode v2.0.29 `run --help` confirms `--standalone`, `--model provider/model#variant`, `--file`, `--format json`.
- Read-only model check `strata/flash-next#fast` returned STRATA_READY. No application implementation delegated yet.
- `/Users/dodgerbluee/dev/plant-tracker` did not exist before this planning work; created only plan docs. No applicable ancestor AGENTS.md found in checked ancestor paths. Existing unrelated repositories not changed.

## Decisions and alternatives deliberately rejected

| Decision | Reason | Reconsider only when |
|---|---|---|
| Own application rather than extend HortusFox | User prefers custom Angular/Go product, cohesive UX and domain/test boundaries. | Explicit product direction changes. |
| Modular monolith | Small collection, one owner; straightforward transactions/tests/deployment. | Measured independent scaling or team ownership warrants split. |
| Postgres + typed SQL | Constraints/transactions and durable jobs; explicit performance. | Deployment constraints materially change. |
| Private filesystem first | Single-host ownership and simple backups; avoid unnecessary object service. | Multiple hosts or media scale needs object store. |
| Page-scoped signals, no normalized cache | Easy-to-test server authority, limited collection size. | Measured cache/relationship complexity justifies framework. |
| Asynchronous analysis + polling | Long/optional providers; no WebSocket auth/reconnect complexity. | Actual interactive streaming need. |
| Reviewed structured care + optional AI | Deterministic care and inspectable evidence; no model-only schedule. | Never remove owner confirmation for irreversible physical actions by accident. |
| Incremental delivery | Journal valuable before AI; R3 cannot contaminate R1 reliability. | No reason to couple journal to provider uptime. |

## Open inputs (not hidden blockers to core implementation)

Owner location/climate, actual cultivars, growing media/containers, owner deployment host/domain, off-host backup destination/key, notification email, desired runtime vision provider/data-sharing preference, and explicit meaning of removing growth. Onboarding/configure later; tests use clearly labeled fixture choices. No source/config choice can establish these personal facts.

Runtime defaults: OCR pipeline uses local Tesseract 5 with sandboxed Poppler rasterization for PDFs plus normalized receipt images; T00 pins/probes runtime binaries and T30 creates editable extraction draft, optionally using configured text model to structure OCR text. A model-readable receipt is still untrusted. Vision adapter uses explicitly configured OpenAI-compatible image-capable chat endpoint behind a provider port, validated by capability probe and T35 evaluation; no particular model assumed to exist locally. Future hosted specialist adapters are optional, not prerequisite code forks.

## Review evidence

Independent grades are archived in `reviews/`. Reviewers are instructed to grade artifacts honestly, identify P0/P1 blockers, and use a weighted rubric. Passing grade indicates plan readiness, not completed application or correctness proof. Implementation tasks need subsequent runtime/visual/restore evidence and independent code review.
