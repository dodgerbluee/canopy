# DEP03 — Socket-free processing

Status: IN_PROGRESS.

### DEP03 — Socket-free bundled processing and v1.1.1

Depends: PUB02.

Own: `server/internal/media/processor/`, `server/internal/insights/receipt_process_test.go`, `server/internal/platform/`, `server/cmd/worker/main.go`, `Dockerfile`, `docker/canopy-run`, `deploy/compose.yaml`, `docs/deployment.md`, `scripts/release/`, `scripts/e2e_with_worker.sh`, `.github/workflows/`, `docs/plan/tasks/DEP03.md`, `.artifacts/agents/DEP03/`. Integrator owns graph/ledger.

Acceptance: Default app + PostgreSQL deployment has no Docker socket, host work directory or runtime Docker CLI requirement. Run installed codecs/OCR as bounded unprivileged subprocesses; preserve formats, orientation, capture dates, derivatives and receipt extraction. Context cancellation kills codec process groups. Publish tested v1.1.1 and promote its verified digest to latest. Preserve local production data/configuration.

Verify: Runner tests for command restrictions, path mapping, bounded output, missing tools, cancellation and resource limits; real HEIC/JPEG/WebP/PNG and PDF/image OCR in the combined image without socket mounts; actual queued photo job, capture date and restart smoke; release CI and AMD64/ARM64 manifests.
