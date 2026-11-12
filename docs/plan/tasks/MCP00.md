# MCP00 — MCP implementation plan

Status: ACCEPTED.

Plan only. User requests exposing Canopy to Grok Bot and Hermes agents. Inspect current contracts/auth/application services and verify client/protocol support against primary sources. Produce `09-mcp-implementation.md` with ordered implementation packets; do not enable remote access or change runtime configuration.

Delivered: `docs/plan/09-mcp-implementation.md`, with 12 proposed sequential packets, a read-only checkpoint, full domain capability map, service/actor/transport boundaries, scoped integration credentials, direct logging vs reviewed changes, retry/atomicity, async revocation, media/EXIF handling, deployment and testing. Packet IDs M01–M12 remain proposals, not registered implementation tasks. MCP00 acceptance means the planning artifact is complete; no MCP functionality has been built.

Evidence: `.artifacts/agents/MCP00/research.json` records primary sources and confirms neither target CLI is installed locally; no live agent compatibility test is claimed. `plan-check.json` records local source link validation; `plan-review.json` records a plan coverage audit, not application tests. `plan-gates.log` records executed repository DAG/packet checks. Existing runtime, provider settings and owner records were not changed.
