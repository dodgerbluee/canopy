> Owner-approved revision, 2026-11-02: the standalone mock at `/Users/dodgerbluee/dev/canopy-design-mock` is now the visual reference for UI01. Its warm light canvas, forest navigation, restrained surfaces and photographic hierarchy supersede the original visual token choices below. Existing interaction, privacy, source-backed care and accessibility requirements continue to apply. Production uses owner uploads and genuine records; no stock/demo content is migrated.

# Visual and interaction specification

## Design intent

A precise botanical observatory: photographic, calm, fast, and technically sophisticated. The advanced feel comes from excellent typography, spatial consistency, rich comparisons, smooth state changes, and legible evidence. Avoid a generic admin dashboard, ornamental charts with no real data, excessive neon/glass, or dozens of tiny statistic cards.

Custom visual language; Angular CDK provides interaction mechanics without prescribing a skin. Ship both light and dark themes; respect system preference with owner override. Dark is used for demo/review screenshots, not forced on every user.

## Design tokens

- Dark surfaces: canvas #0B1210, base #121D18, raised #19271F, border #34483B. Primary text #F3F7F1, muted #ADBCAF; interactive accent #B8F171 with dark foreground #16200C. Warning #F1CA7C, danger #FFB1AA. Validate final combinations rather than trusting hex values.
- Light surfaces: canvas #F5F7F2, cards #FFFFFF, primary text #17291D, muted #526457, accent #315C19. Borders and nontext indicators must remain visible without shadows.
- Typography: self-hosted variable sans (e.g. licensed Manrope) for UI/headings; tabular digits for dates/metrics; restrained monospace for provenance IDs only when expanded. Headings 32/40 desktop, 26/32 mobile; body 15–16/24. No text below 12 px for essential information.
- Spacing scale 4, 8, 12, 16, 24, 32, 48, 64; content max-width 1440 px; 24px desktop and 16px mobile gutters. Radii 10px fields, 16px cards, 24px hero panels. Controls minimum 44px touch target.
- Elevation through surface/border first, subtle shadow second. Status uses icon + label, never color alone. Focus outline is persistent 2px high contrast with 3px offset.
- Motion: 120–180ms controls, 200–260ms page transitions; transform/opacity only. Reduced-motion disables slide/zoom, parallax, and auto-animated comparison. No ambient particles or animated dashboard loops.

Tokens defined as CSS custom properties; components cannot add new arbitrary colors/radii without design-system update. Review contrast >=4.5:1 text, >=3:1 large/nontext controls, WCAG 2.2 AA interactions.

## Information architecture and routes

| Route | Purpose |
|---|---|
| /login; /onboarding | Private login and first workspace setup, no auth data in URLs. |
| /today | Actionable care, recent journey, weather context, unresolved suggestions. |
| /plants | Photo-led collection with search, tags, location/status filters, grid/list. |
| /plants/new | Simple stepwise creation with optional details, no mandatory encyclopedia lookup. |
| /plants/:id | Plant overview and chronological journey; tabs preserve URL state. |
| /plants/:id/compare?left=...&right=... | Reproducible photo comparison selection. |
| /journal | Collection-wide timeline with type/plant/date filters. |
| /purchases; /purchases/:id | Purchase list, allocation editor, original receipts. |
| /care | Agenda and care plan editing; original vs effective dates visible. |
| /insights; /insights/:id | Suggested checks and evidence detail. |
| /settings | Workspace, locations, privacy/provider capabilities, units/theme, export jobs. |

R1 release starts at `/plants` and shows Plants/Journal/Purchases/Settings only. `/` redirects to `/plants` until R2 enables Today/Care. Unavailable routes return a useful release-aware page, never blank placeholder or fake data; hide Insights until evaluated R3 capability is configured. Feature navigation derives from server release capabilities.

Desktop: 224px left nav, compact top bar with location/date and quick-add. Tablet 768–1199px: collapsed rail, panels flow to two columns. Mobile <768px: bottom navigation Today/Plants/Journal/Care/More plus thumb-reachable Add action. Navigation landmarks, active state, descriptive page titles. Drawer is enhancement to navigable page so deep links, back button, refresh work.

## Screen composition

**Today:** one clear “Today in your collection” heading, due count, useful context. Primary area is grouped checks with compact complete/snooze actions; right area desktop shows current conditions with freshness and recent photo. Lower area recent journey and proposals. Avoid showing seven empty charts for seven plant records. No conditions until location configured; explain once and link settings.

**Collection:** visual cards with real photo, name, cultivar/unknown badge, location, next check. Filters in URL; toggles preserve scroll. Active/archived/dead views. Cover skeleton retains aspect ratio. Empty state has one clear add action and optional starter collection, not fake examples masquerading as live plants.

**Plant detail:** wide hero photo/identity; meaningful facts (source, acquisition, location, growth context) beside it; tabs Journey/Care/Purchases/Insights. Persistent quick log and add photo. Journey has date groups, filter chips, mixed event cards, paginated loading. Original date/source badge available. Care shows the owner plan and why it applies. Purchases show attributed spending and unknown-cost count. Insights show proposed/accepted/dismissed clearly.

**Capture:** mobile file picker with camera hint and multiple selection. Per-file queue, preview, plant/date/viewpoint assignment, progress, processing, retry/remove. Failed item never loses successful siblings. Server retains RESERVED/UPLOADED/PROCESSING assets and owner-scoped pending capture metadata for 24h; session bootstrap/query returns pending uploads after reload. No file bytes or receipt text persist in browser storage. Fully received uploads can finalize/retry processing without reselection. Incomplete uploads restart from byte zero after explicit file reselection; browser verifies size/checksum against reservation to avoid wrong-file attachment. Failed checksum creates a new reservation; idempotency prevents duplicate links. Owner may abandon pending assets; sweeper expires them. No resumable-chunk protocol is promised in v1. Resizing/processing server-side; local preview revoked on close. Accessible progress and error association; navigation warns only for genuinely unsaved work.

**Comparison:** side-by-side is default; optional overlay slider with keyboard arrows/Home/End, visible handle, buttons for left/right selection. Equal viewport/aspect-fit with independent crop toggle, not stretched images. Dates/captions persist, difference in viewpoint disclosed. No auto-generated growth percentage.

**Purchase editor:** seller/date, line items, exact totals, allocated plants, overhead handling, reconciliation summary. Receipt displayed as image or PDF download. OCR draft highlighted as unconfirmed; no copy-paste JSON UI. Save disabled only with specific inline reason. Owner can correct every extracted value.

**Care:** agenda with overdue/today/upcoming grouping; optional month view secondary. Each entry distinguishes “check” from “perform action”; reason and guidance source expandable. Plan editor previews next five dates and timezone, and what an edit cancels. Complete/skip/snooze offer immediate confirmation and correction through activity UI, which routes linked task completions to correctOccurrenceCompletion and its date-change preview (ordinary unlinked activities use edit/void); do not pretend an email has been unsent.

**Insight:** structured evidence sheet: what is visible, what changed, possible explanations, missing facts, next check. Photo references open exact comparison. Citation link shows publisher/date and region. Accept makes a task after preview; dismiss asks optional reason. “Analysis unavailable” explains missing capability and preserves manual observation flow.

## State and accessibility contract

Every page/story fixture covers initial loading, empty, ready, partial failure, offline/server error, stale data, validation conflict, and permission/session expiry where relevant. Skeletons reflect actual layout; errors include retry and correlation ID when useful. Never replace a failed query with an empty collection. Route changes cancel requests. Session expiry preserves nonsecret draft locally in memory for one login cycle and prompts reauthentication; do not persist receipt text or photos in localStorage.

Dialogs trap/restore focus, Escape closes only when safe, have accessible names. Menus support keyboard navigation. Toasts do not steal focus; successful saves use polite live region. Forms expose label/hint/error relationships and focus first invalid field. Tables work at 200% zoom; no page-level horizontal scroll at 360px. Timeline uses semantic headings/lists, not an inaccessible canvas. Charts, if introduced for measured data, have equivalent table.

## Visual quality gate

T05 builds a static composition with synthetic fixtures for Today, collection, detail, capture, compare, purchase, insight before production feature assembly. Capture screenshots at 390x844, 768x1024, 1440x1000 in both themes. Reviewer checks hierarchy, density, alignment, type, photo treatment, error states, and keyboard access. User preference may refine it during implementation; no mandatory approval pause is introduced.

Later visual regression captures fixed browser/fonts/clock/media fixtures, masks only volatile timestamps not layout, and permits baseline changes only with before/after review. A local text-only coding model cannot certify visual polish; browser screenshots and independent human-capable visual review are mandatory evidence. UI quality cannot be reduced to component/unit test counts.

### UI02 — common data-entry dialogs

Garden record entry uses the shared `UiEntryDialog` over CDK overlays. Native selects, dates, radios and checkboxes remain native. Forms share headers, field spacing and action rows; dirty exits ask before discarding, in-flight saves block dismissal, and successful saves return to refreshed parent screens. Session expiry can leave a busy/dirty editor for sign-in. Uploads retain their existing recovery pipeline; completed attachments do not count as unsaved drafts. Receipt extraction and analysis remain explicit requests; applying extracted receipt values fills the purchase draft without saving it.


Photo framing (UI04): Plant cards and identity/dashboard heroes use bounded local foliage detection to place a non-destructive crop around the central plant canopy. Resize recomputes cover geometry from cached focus. Analysis does not use external providers or persist pixel samples. Documentary gallery/journal use full-image containment to retain the entire photo. The original uploaded media, selected cover photo identities and EXIF dates remain authoritative; cropping changes presentation only.


Collection filters (UI05): Keep search in the page toolbar and present Status, Category, Sort order and Layout as matched native fields in one side panel. Reset filters preserves search text; Done closes after immediate URL-backed changes. The panel/focus trap exist only while open. Collection creation is a single dashed outline Add Plant tile after the real rows, including loaded empty results; it opens the existing modal route and is not counted as a plant. Loading and failure retain their dedicated states.


Native choices and Today (UI07): Single native select.input fields share themed trigger typography,44px size, chevron and focus/disabled/invalid states. Supporting browsers style the real native option popup; others retain platform choice behavior with a consistent trigger. Escape closes an open picker before dismissing its dialog/drawer. Your garden opens /plants; Settings remains explicit. Today rotates existing plant covers on a new visit or hard refresh, remembers the last shown plant per workspace/tab, and stays stable during same-page actions. Headline uses the recorded plant name.
