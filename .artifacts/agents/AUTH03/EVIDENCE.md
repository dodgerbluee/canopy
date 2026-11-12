# AUTH03 executed verification

Root cause: setup did not instantiate UiTheme, so it stayed on the light CSS
fallback. Moved initialization to app startup and applied browser/explicit
preference in the document head before styles paint. Browser dark preference
is detected directly; no preference defaults to light. Default choices are not
persisted. Explicit overrides remain respected, and selecting System resumes
following the browser. Native controls receive the resolved color scheme.

Also made the setup introduction more compact: icon beside heading and shorter
supporting text. This moves the form visibly higher on phones while preserving
consistent field spacing.

Executed all 490 Angular unit tests (including five theme regressions): pass.
Production combined image build: pass. Refreshed the existing local Docker
preview without changing database/storage volumes or submitting setup.

Actual-browser checks on the served setup page passed for dark and light
preferences, live browser changes, no stored default, saved explicit override,
and dark theme before Angular scripts load. Confirmed current served spacing
and inspected dark-mode screenshot. Read-only layout/axe checks at 1440, 768,
390 and 320px also pass with no overflow or accessibility violations. Plan,
boundary and diff whitespace checks passed. Detailed ignored logs/screenshots
are beside this summary. Preview: http://localhost:7081/setup.
