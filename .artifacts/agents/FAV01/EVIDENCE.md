# FAV01 — Safari favicon compatibility

Implementation/local verification complete; release/deployment pending.

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
