# AUTH04 execution evidence

Replaced the two-column setup layout with a modern, narrow registration card.
The four account fields are full width in a single column at every viewport.
Garden name, timezone and currency are behind a native, keyboard-accessible
Garden preferences disclosure, initialized with the existing valid defaults.
Invalid hidden preferences automatically reveal their section. Backend API and
browser-driven theme behavior are preserved.

Executed five focused setup tests: pass, including default-preference submission
and revealing an invalid hidden preference. Production combined image build:
pass. Refreshed only the existing preview application service without resetting
its database or storage.

Read-only Playwright checks passed at desktop 1440px (dark/light), phone 390px
(dark), and narrow phone 320px (light). Verified four vertically ordered,
aligned account inputs, collapsed preferences, working expansion, retained
My garden/USD defaults, browser-selected theme, and no horizontal overflow.
Axe reported zero violations with preferences both closed and open at every
size. Corrected the footer landmark identified by the first accessibility run.
Inspected desktop and phone screenshots. No registration form was submitted.

Plan, boundary and diff whitespace checks passed. Detailed ignored logs/JSON
and screenshots are beside this summary. Preview: http://localhost:7081/setup.
