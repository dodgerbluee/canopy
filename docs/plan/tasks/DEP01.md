# DEP01 — Container publishing and release promotion

Status: ACCEPTED.

Adapted Docked's development / tagged-release / manual-latest flow. Main/master publishes commit-specific development image sets, then advances dev aliases only after the full set succeeds. Version tags publish stable/prerelease images and GitHub deployment releases. Manual promotion validates a stable release manifest, checks all version tags against its recorded digests, retags those exact existing manifests as latest without rebuilding, verifies each alias, then marks the GitHub release latest. Production environment reviewer configuration is documented, not presumed. Existing version tags are refused; partial failures require failed-job retry or a new version. Registry alias updates are not atomic across three packages; that limit is explicit.

Reusable publication gates call existing CI, build and smoke-test a real disposable deployment, then publish linux/amd64 and linux/arm64 server/web/media images with provenance/SBOM and OCI source/revision labels. The server image contains matched API/worker, migration/admin and MCP helper binaries; web uses Node 24 and both locked dependency trees, then Caddy TLS/proxy/SPA hosting. Media retains the pinned sandbox Dockerfile. Docker context excludes local data, credentials, caches and test fixtures.

Compose provides TLS-verified PostgreSQL, separate public/private database certificate volumes, one-shot initialization/migrations, private API/DB network exposure, shared private media/encryption-key storage and UID 10001 application processes. Only the worker receives the Docker socket. Its temporary workspace is bound at the same absolute host/container path, and the matching media image is prefetched. Web serves HTTPS and forwards app/MCP/upload/export routes with same-origin authentication. The deployment guide covers initial owner creation, GHCR access, AI configuration, MCP, upgrades/backups and schema-aware rollback.

Executed local evidence in `.artifacts/agents/DEP01/`:

- `actionlint.log`: workflow lint passes. CI also runs pinned actionlint and release-helper tests.
- `release-tests.log`: seven tests pass for strict SemVer/dev identities, injection rejection, deployment bundle membership/checksums, exact-digest promotion and drift rejection before any alias write.
- `server-build.log`, `web-build.log`, `media-build.log`: all three actual images build successfully. Frontend clean-room build uses Node 24 with both contract and web lockfiles. Native-platform artifacts were built locally; the two-platform publishing matrix is configured but a GitHub registry run is not claimed.
- `smoke.log`, `smoke.json`: actual isolated production-config Compose deployment passes DB verify-full TLS, owner login, SPA deep link, MCP unauthenticated rejection, original Android photo upload, queued sandbox processing, EXIF capture-date attach and private JPEG derivative. A second smoke passes after final image/helper changes. Unique project/volumes/temp files removed; development garden credentials/storage/AI never used.
- `registry-inspect.log`: raw registry-manifest hashing verified against the registry's reported digest. This caught an unreliable Go-template digest lookup and promotion now hashes exact raw manifest bytes.
- `plan-boundary.log`: plan/packet/dispatch and import-boundary checks pass.

No Git remote is configured in this checkout. Workflows/files are prepared locally; no commit, registry push, GitHub release, DNS/tunnel or public deployment was performed. Existing local API/worker/frontend processes remain unchanged. Configure the GitHub remote, commit/push the intended code, set package visibility/access, and configure a production hostname before using the published deployment. The workflow itself enforces CI and smoke gates when run on GitHub; full remote CI execution is unverified here.
