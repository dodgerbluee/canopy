# DEP03 — Socket-free processing

Local implementation and validation completed; release verification pending.

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
