# Public acceptance record: UI17

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| UI17 | 2026-11-04 | Rounds grid separated from authenticated full-screen plant actions; Back/browser-back restore grid position and focus, uploads survive route changes.38 focused/417 full units, warning-free production build,13 browser cases(axe0/errors0/overflow0) and local/phone list/deep-route checks passed. | .artifacts/agents/UI17/SUMMARY.md |

# UI17

Split Rounds into a photo grid and a separate full-screen plant action view with Back to rounds.

Completed: /rounds is grid-only; /rounds/:plantId is a standalone authenticated
full-screen action view with Back and no sidebar/app menu/list. Grid scroll and
focus restore on return.38 focused/417 full units, warning-free production
build and13 browser cases passed (axe0/overflow0/unexpected-errors0), including
Back/browser-back, cold/deep/unknown routes, auth expiry, long-list restoration
and continuing photo saves. Existing local/phone previews updated.
Evidence: .artifacts/agents/UI17/SUMMARY.md.
