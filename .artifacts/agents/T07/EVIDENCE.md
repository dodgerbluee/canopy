# Public acceptance record: T07

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| T07 | 2026-11-01 | unit suites + real-stack Playwright e2e + full gate | `.artifacts/agents/T07/validation.txt` | 77 web unit tests (session/guard/login/shell/settings); 8 e2e on real Go API+disposable schema (login/refresh/deep-link/back/logout, SQL-aged idle expiry with honest notice, both themes axe-clean, no secrets in browser storage); SESSION_EXPIRED vs UNAUTHENTICATED distinction; test-e2e flipped to real suite in check gate |
