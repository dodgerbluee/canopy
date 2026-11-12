# MIG01 — Local production migration

Status: ACCEPTED — migration and owner registration complete.

Source: legacy localhost:4311 UI, host API/worker, PostgreSQL 17 public schema
in canopy_t02, storage in the original plant-tracker checkout. Preserve source
and private configuration. Target: combined app and dedicated PostgreSQL 17,
keeping localhost:4311 as the production entry point.

Handover uses a private, expiring one-use registration capability prepared by
the operator CLI. Link tokens live in URL fragments (never HTTP request URLs),
exchange for an HttpOnly SameSite cookie, and are consumed atomically when the
user chooses username/password. The primary account is re-registered while
owner/workspace/entity IDs stay unchanged. Existing sessions are revoked and
public registration stays closed. No user password is chosen by the agent.


Permanent operator location: `/Users/dodgerbluee/dev/canopy-local/` (private
compose/env files, registration opener and pre-migration backup). Source legacy
API/worker and UI containers were stopped gracefully, not deleted. Source DB
and storage remain retained/frozen. The target preserves localhost:4311,
localhost:8080 API/MCP and the existing phone address on port 4313.


Executed evidence: `.artifacts/agents/MIG01/EVIDENCE.md`. Complete restored-copy
browser registration, private photo, live AI test and queued export passed.
Production core table fingerprints and all 408 original files match the frozen
source. Source/target backup checksums verified. Production worker completed
real care, weather, knowledge and media jobs. All original local URLs are
healthy. The protected registration page was opened for the user; no production
password was generated or entered by the agent.
