# FAV01 — Safari favicon compatibility

Status: ACCEPTED. Released and deployed.

User reports a Safari browser-tab favicon failure. Existing referenced SVG and
PNG-compressed ICO are served as valid images. The deployed reverse proxy gives
icons long cache lifetimes, while unrecognized icon paths return SPA HTML. This
establishes compatibility/cache weaknesses, not a reproduction of the user's
specific Safari UI state. No browser profile/cache/preferences were changed.

Implemented content-addressed icon URLs, explicit 32px PNG fallback, traditional
BMP-frame ICO at 16/32/48px, SVG sizes=any, 180px Apple touch icon and monochrome
pinned-tab mask. Preserved the existing sprout artwork/colors. Canonical fallback
paths remain available. Caddy returns 404 for absent icon files instead of HTML.

Executed Node24 production build; actual Chromium and Playwright WebKit decoded
all five assets at their declared raster dimensions. Visual inspection confirmed
the brand mark. This is WebKit engine verification, not a claim that the user's
native Safari tab was observed. Real Caddy requests verified icon MIME types and
missing icon 404. Seven release-tool tests, plan/boundary checks and disposable
combined-image smoke passed, including the new favicon check, queued photo
processing, original capture date, private media and supervised worker restart.

Private remote stack/configuration backup and resolved-config checks are stored
outside source control. Raw browser reports, screenshots, generation helpers,
image build and smoke evidence remain ignored beside this summary.

An additional pre-publication review checked [Apple's pinned-tab requirements](https://developer.apple.com/library/archive/documentation/AppleApplications/Reference/SafariWebContent/pinnedTabs/pinnedTabs.html).
The monochrome mask now uses a 16x16 viewBox and one scaled black path.
The earlier v1.1.3 candidate was cancelled before any image publication;
release verification will use a fresh v1.1.4 tag.

Release v1.1.4: https://github.com/dodgerbluee/canopy/releases/tag/v1.1.4
Full release run 43361523777 and promotion 43363433071 succeeded. Source revision
9f2507089b964078ac9e3b16d29b984be667aa7a. CI passed 493 frontend tests, Go/PG17
integration, build/generation/plan gates and the actual image smoke including
favicon assets/404, queued photo processing and worker restart recovery.
Published asset checksums and anonymous AMD64/ARM64 manifests were verified.
Version and latest match:
sha256:80c3c38fcf8dae1cf5a45cf6f9c645bd1aec559e9d381f2fa9bb295707e282e1.

Privately backed up the live Canopy Portainer definition and container settings.
Verified the proposed resolved Compose changes only the application image. Pulled
and checked the expected digest/revision, gracefully stopped the app, captured
consistent counts, recreated only Canopy and verified readiness. The PostgreSQL
container ID, complete application environment, bind mounts and core counts
match their pre-deployment values (17 plants, 80 photos, 86 assets, 4 purchases,
one registered account). The user's additional photo since migration remains
present. Persisted the 1.1.4 version in the original Portainer stack file and
verified its bytes; kept rollback files privately. No DB migration/data rewrite
or login/password change was introduced by this frontend fix.

Post-deployment requests through the actual public site returned all five icon
files with exact build hashes and image MIME types, including PNG dimensions and
traditional BMP ICO frames. A new missing-icon URL returned 404. Chromium and
Playwright WebKit decoded every live icon, with all resource responses HTTP 200.
This does not establish the state of the user's native Safari UI/cache. The
user should reopen its tab to refresh the icon mapping. Temporary local preview
was available during work and removed after deployed verification.

Raw release logs, browser screenshots/results, private operator helper,
configuration backup, expected digest and deployment reports remain ignored.
No private operator host, environment, credentials, DB or media is in this summary.
