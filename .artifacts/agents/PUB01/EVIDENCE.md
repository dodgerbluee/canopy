# PUB01 — First GitHub repository and v1 release

Status: ACCEPTED.

Created private https://github.com/dodgerbluee/canopy and published a clean source checkout at /Users/dodgerbluee/dev/canopy. The original plant-tracker history contains private imported garden artifacts and remains local. A clean initial history avoids publishing that data. Only text-only acceptance summaries are retained publicly in the source snapshot; original local execution artifacts remain private. Current CI reproduces all release verification gates.

Gitleaks scans the clean source with zero leaks. Initial publication exposed an overly broad ignore rule that excluded scripts/release; narrowed it to root build-output folders. Full CI then found the Today consumer's stale evidence-label assertion after the suggestion UX change; replaced it with record-count and collapsed-disclosure assertions. No test gate was removed. Failed, still-unpublished v1 tags were corrected before any images were published; published v1 was not retagged.

Final release: https://github.com/dodgerbluee/canopy/releases/tag/v1.0.0
Build: https://github.com/dodgerbluee/canopy/actions/runs/43150747371
Release revision: 97a254ea97432097abaa4d8848f2e6b74a96e72c

Executed evidence under .artifacts/agents/PUB01/ in the original local checkout:

- secret-scan.log / secret-scan.json: zero credential findings in the clean source.
- generation.log / clean-plan.log: clean checkout generation has no diff; plan/packet/boundaries pass with honest acceptance summaries.
- frontend-tests.log / backend-tests.log: all 477 frontend tests and the complete Go suite pass locally with disposable PostgreSQL schemas and sandboxed codecs.
- final-release.json / github-release.log: every remote stage succeeds — authoritative generation, lint, unit/integration tests, actual production Compose smoke, both-platform server/web/media publishing, deployment bundle and GitHub release.
- release-verification.json / release-assets/: downloaded checksums verified, complete three-image digest manifest bound to the exact v1 commit, deployment tar includes only Compose, empty-secret env template, README and image manifest. Both-platform publication completed on GitHub.

The v1 images remain versioned as 1.0.0; no latest promotion or public DNS/deployment was performed. Original local garden/API/worker/storage were preserved. Native Hermes/Grok account setup remains outside this release verification. A follow-up housekeeping commit records this result and corrects the Go cache dependency path for future workflows; its YAML is linted locally.
