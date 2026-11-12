# Public acceptance record: M02

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| 2026-11-06 | M02 | ACCEPTED | Scoped expiring agent grants and hashed credentials; real PostgreSQL tenant, rotation and revocation tests. Evidence: `.artifacts/agents/M02/`. |

# M02 — Agent grants and credentials

Status: ACCEPTED.

See step 2 in [MCP implementation plan](../09-mcp-implementation.md).

Evidence: `.artifacts/agents/M02/access-tests.log`: real disposable PostgreSQL verifies hashed credentials, cross-workspace denial, rotation/revocation; unit scope/rate tests pass.
