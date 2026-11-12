# PUB02 executed publication evidence

Published accepted registration, users, OIDC, browser-theme and protected
migration-handover source to the private dodgerbluee/canopy repository.
Verified completed production registration and preserved garden before release.
No local garden database, media, configuration, keys, credentials, browser
output or original private development Git history was published.

Executed preflight: scanned 915 source files against five known private runtime
values, with zero matches; browser output/private runtime paths excluded.
Generation leaves no diff; plan/boundary, workflow actionlint and seven release
tooling tests pass. The deployment bundle now pins its exact released version
and image prefix, verified with a non-default repository/version fixture.

Release v1.1.0: commit b60b5a0814a54e800a48c21cf10d7791a7cea799.
GitHub release run 43297238163 succeeded: full CI (493 Angular tests, Go unit
and PostgreSQL integration suites), native combined-container smoke including
queued photo processing/capture date/private preview/restart, AMD64+ARM64 image
publication and release bundle creation. Downloaded release checksums matched;
manifest revision and single app image digest verified. The archived Compose
has exactly Canopy + PostgreSQL, no source build, correct version defaults and
blank credential placeholders.

Promotion run 43301368789 succeeded. Its registry checks compare the immutable
version tag with the release manifest and verify that latest has the identical
digest, without rebuilding. GitHub's latest-release API returns v1.1.0.
Image: ghcr.io/dodgerbluee/canopy:1.1.0 (also latest).
Digest: sha256:df12e88ba7368f3395bc29c9ce387c110d9ef9ec92c2f9fe90bb904406184dcb.
Release: https://github.com/dodgerbluee/canopy/releases/tag/v1.1.0

Detailed ignored logs and downloaded release assets are beside this summary.
The running local production garden was not redeployed or reset by publication.
