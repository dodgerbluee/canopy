# DEP02 execution evidence

The combined image packages the existing frontend, API, worker, migrations,
operator CLI, MCP helper and codec tools. Compose defines only `canopy` and
`postgres`. Application Go and Angular behavior was not redesigned.

Executed checks:

- Combined image built locally; disposable two-service smoke passed startup,
  first-owner login, SPA deep link, MCP authentication, a queued photo upload,
  capture-date retention, private derivative delivery and supervised worker
  failure/restart with the original owner and stored photo retained.
- The same image rendered a synthetic receipt PDF and Tesseract read its known
  total in a network-disabled temporary codec container.
- Seven release metadata/bundle/promotion tests passed.
- Shell syntax, workflow actionlint and plan/boundary checks passed.
- GitHub release run 43246631362 passed the complete CI suite and bundled
  container smoke, AMD64/ARM64 publication, bundle and release creation.
- Downloaded v1.0.1 release checksums matched. Its manifest identifies only
  `ghcr.io/dodgerbluee/canopy` at the released revision; the archived Compose
  matches source, contains exactly two services and requires no source build.
  Credential placeholders remain blank.

An earlier remote smoke run exposed a restart/readiness race. Startup now waits
for its children before reporting ready; the recovery assertion waits for
healthy status and verifies two successful reads without allowing repeated
restarts. The corrected local and GitHub smoke checks passed.

Local detailed logs and JSON results are ignored artifacts in this directory;
they contain only disposable test data. No live garden data was used or changed.
