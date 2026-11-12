# DEP03 — Socket-free processing

Status: ACCEPTED. Local and release validation completed.

Executed: Go tests for processor/insights/platform/worker, go vet for those
packages, seven release-tool tests, actionlint, plan/packet/dispatch/boundary
validation, combined ARM64 image build, real in-image process/corpus/OCR tests,
and the disposable two-service deployment smoke.

Real tools verified HEIC orientation, JPEG/PNG/WebP derivatives with metadata
stripping, corrupt input and pixel-bomb rejection, PDF/PNG/JPEG receipt OCR,
OS resource limits, bounded output, stripped credential environment and deadline
cleanup of descendant processes. Queued photo processing retained capture date;
private preview and worker restart recovery passed with no socket or host bind
mounts. The combined image has no Docker CLI. Legacy Docker support is optional
for existing host-development harnesses only.

Raw logs, test executables and smoke report remain ignored in this directory.
No production records or operator configuration were modified.

The first tag (v1.1.1, run 43321895998) stopped before image publication: the
cancellation assertion raced asynchronous SIGKILL delivery on the Linux CI host.
The test now polls for actual termination for at most one second and still fails
if a live descendant remains. The corrected test passed thirty repeated runs in
the unprivileged, network-disabled image. The new release candidate is v1.1.2;
no failed tag or existing image was overwritten.

Release v1.1.2: https://github.com/dodgerbluee/canopy/releases/tag/v1.1.2
Source revision: fc96f57866502ca43e5dabaa725802a7cedc67a6.
Successful full release run: 43322860660. Successful latest promotion: 43325271507.
GitHub CI passed generation, plan/boundary checks, lint, 493 frontend unit tests,
Go tests and PostgreSQL integration tests. Bundled AMD64 tests passed all codec
limits, cancellation, real phone-photo formats and PDF/image OCR. The actual
socket-free queued-photo/restart smoke passed. Both AMD64 and ARM64 images were
published. Release asset checksums/revision and the two-service socket-free
Compose archive were independently verified after download.

An actual Docker pull with an empty credential configuration succeeded for
1.1.2. Anonymous registry requests confirmed version and latest are identical:
sha256:738ec56a2202544d8819cf572d5ac5dca6379456c57e89803be4e9335d568abd.
The source main branch is synchronized with a text-only acceptance commit;
no production database, media, operator configuration or runtime was changed.
