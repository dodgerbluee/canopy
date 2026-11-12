# Public acceptance record: UI32

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| 2026-11-11 | UI32 | ACCEPTED | Compact action-first suggestions with disclosed context, grouped evidence counts, exact readable money and Add to care confirmations. 16 affected tests, production build, five responsive accessibility flows and actual read-only Meyer lemon phone/desktop checks pass. Published locally. Evidence: `.artifacts/agents/UI32/`. |

# UI32 — Readable plant suggestions and evidence disclosure

Status: ACCEPTED.

Plant suggestions now appear as compact action-first rows in one section, with a small category icon, clear action and Add to care / Dismiss choices. The spending review has a short title; the full original action remains available in its detail disclosure and confirmation. A single section explanation replaces the repeated acceptance disclaimers. Mobile keeps Why, Add to care and Dismiss together on one row, with consistent touch targets.

Why this suggestion uses native accessible details/summary disclosure. Recorded context, possible explanation, supporting records, unknown context and expiry are retained there instead of repeated in the visible list. Supporting IDs are deduplicated and summarized by type/count; the two-photo comparison remains one usable link. When a matching run manifest is unavailable the count honestly says records. A different run's evidence is never used to classify suggestions.

Explicit integer minor-unit expressions render using the existing exact money formatter (13900 USD minor units becomes 139.00 USD). Ordinary amounts, dates, negative or decimal malformed expressions remain unchanged; proposal text and mutation payloads are not rewritten. Acceptance/dismissal still use the shared modals, selected due date, optimistic revision, changed-source review confirmation and server error behavior. Cost acceptance still creates a check task; it does not edit purchases.

Executed evidence in `.artifacts/agents/UI32/`:

- `frontend.log`: 16 affected Angular tests pass, including closed context, grouped evidence/comparison link, readable money, creation/review/conflict flows, explicit acceptance and dismissal. Final production build completes with Node 24 Docker and no style-budget warnings.
- `browser-report.json`, `browser.log`: five 320/390/1440 phone/desktop light/dark layouts pass; collapsed context, exact amount display, add/cancel/due-date acceptance, explicit dismissal and no horizontal overflow. Zero axe violations and browser errors. Mutations use synthetic fixtures only.
- `suggestions-*.png`, `context-*.png`: reviewed compact and expanded states across those sizes/themes. Fixed shell chrome is hidden only for element screenshot capture.
- `live-report.json`, `live-browser.log`, `live-suggestions-*.png`, `live-context-*.png`: actual authenticated Meyer lemon list on phone and desktop; context disclosure and modal open/cancel work, no garden mutations, no overflow, zero scoped axe violations/browser errors.
- `published.json`, `local-release.json`: final frontend copied to desktop, progress and LAN phone previews with index replaced last and old chunks retained. API remains ready. Temporary task QA container stopped.
- `plan-boundary.log`: plan/packet/dispatch and import checks pass.

Frontend only. Existing API/worker, AI connection, stored advice and care/purchase records are preserved. No new analysis was requested and no suggestion accepted or dismissed in the real garden.
