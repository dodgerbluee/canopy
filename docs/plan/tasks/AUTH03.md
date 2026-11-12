# AUTH03 — Browser theme defaults

Status: ACCEPTED.

Fixed missing setup theme initialization by applying the preference before first
paint and initializing UiTheme globally. The browser is the default, live
changes are followed and explicit overrides remain respected. Tightened setup
intro layout so the form begins higher on mobile. User confirmed the viewed
route is localhost:7081/setup.

Verified: 490 Angular tests, production image build, real-browser light/dark,
live updates, saved override and pre-Angular dark mode. Four-width read-only
layout/axe checks and plan/boundary checks pass. Local Docker preview refreshed
without resetting data. Evidence: `.artifacts/agents/AUTH03/EVIDENCE.md`.
