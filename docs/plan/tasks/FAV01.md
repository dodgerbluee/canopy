# FAV01 — Safari favicon

Status: IN_PROGRESS.

### FAV01 — Deployed Safari favicon

Depends: DEP03, MIG02.

Own: `web/src/index.html`, `web/public/`, `docker/Caddyfile.web`, `scripts/release/smoke.py`, `deploy/compose.yaml`, `docs/plan/tasks/FAV01.md`, `.artifacts/agents/FAV01/`. Integrator owns graph/ledger. Operator scope: privately back up the authorized remote Canopy stack; update only its app image version in the existing Portainer stack and deploy the durable fix with rollback available. Preserve its database, storage, credentials, public URL and sessions.

Acceptance: Existing Canopy brand renders through Safari-compatible raster/ICO fallbacks and modern SVG metadata, with fresh content-versioned URLs. Include Apple touch/pinned-tab assets. Missing icon files return 404 rather than cached SPA HTML. Verify real deployed resources/browser decoding and publish a stable combined image for both supported architectures. Preserve the running garden.

Verify: Node24 production build; Chromium/WebKit image decoding, MIME and intrinsic dimensions; real origin/public static-file requests including missing icon 404; release smoke and CI, anonymous manifest/pull and promotion digest equality. Do not claim user Safari UI verification from Playwright WebKit alone.
