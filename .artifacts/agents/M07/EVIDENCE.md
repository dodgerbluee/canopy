# Public acceptance record: M07

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| 2026-11-06 | M07 | ACCEPTED | Trusted logging and operation replay/audit verified with real PostgreSQL; rain stays separate and maintenance test restores its disposable schema flag. Existing unmeasured-water backend mismatch corrected. Evidence: `.artifacts/agents/M07/`. |

# M07 — Trusted logging and operation replay

Status: ACCEPTED.

See step 7 in [MCP implementation plan](../09-mcp-implementation.md).

Contract clarification authorized by the existing quick-watering workflow and this plan: unmeasured WATER accepts quantity/unit both null; measured quantities retain existing unit/positivity checks. The Rounds UI already sends this shape; no quantity is invented.

Evidence: `.artifacts/agents/M07/logging-tests.log` covers real logging, replay/conflicting IDs, durable audit origin, rain without synthetic waterings, and explicitly schema-qualified maintenance rejection/restoration. Unmeasured watering stays null; measured validation remains strict.
