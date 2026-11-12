# Public acceptance record: M04

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| 2026-11-06 | M04 | ACCEPTED | Authenticated, bounded Streamable HTTP transport, per-request scoped discovery and opt-in API composition. Host/Origin/cookie-only rejection tests pass. Evidence: `.artifacts/agents/M04/`. |

# M04 — MCP HTTP transport

Status: ACCEPTED.

See step 4 in [MCP implementation plan](../09-mcp-implementation.md).

Evidence: `.artifacts/agents/M04/transport-tests.log`; bearer-only boundary, Host/Origin rejection, private caching, compiled opt-in API wiring. No listener enabled on the current runtime yet.
