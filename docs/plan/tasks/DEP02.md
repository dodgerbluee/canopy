# DEP02 — Bundle existing local app into one deployment image

Status: ACCEPTED.

Packaged the existing frontend, API, worker, migrations, operator CLI, MCP helper
and codec tools as `ghcr.io/dodgerbluee/canopy:1.0.1`. The deployment Compose has
only Canopy and PostgreSQL. Existing Docker codec isolation is retained, using
the combined image for temporary jobs. Application Go/Angular behavior and the
live development garden were not changed.

Executed local checks cover startup, migrations, owner login, actual queued photo
processing, capture date, private preview, worker failure and coherent restart,
and a synthetic receipt PDF rendered/read in the existing sandbox. Seven release
tests, shell syntax, actionlint and plan/boundary checks passed.

GitHub release run 43246631362 passed full CI, bundled smoke, AMD64/ARM64 image
publication, deployment bundle and release creation. Downloaded asset checksums,
release revision, single app digest and two-service Compose were verified.

Release: https://github.com/dodgerbluee/canopy/releases/tag/v1.0.1
Evidence: `.artifacts/agents/DEP02/EVIDENCE.md`; detailed ignored logs beside it.
