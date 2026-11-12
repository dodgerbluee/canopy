# MIG01 executed verification

Migrated the actual localhost:4311 garden from legacy host API/worker and
canopy-pg into the combined app plus dedicated PostgreSQL 17. The original UI
containers and explicitly identified Go processes were stopped gracefully after
freezing writes. Original database/storage remain retained and frozen, and a
private consistent database + complete storage backup is preserved in the
permanent operator folder. No source code in the original checkout was changed.

Executed verification:

- Source migration SQL 1–16 matches target. Auth migrations 17–18 applied after
  restoring the complete public-schema PostgreSQL dump, including all existing
  garden, care, knowledge, AI, agent and history tables.
- Restored fingerprints match across 23 core garden tables. Verified SHA-256
  and size of all 408 original storage files, including encryption keys.
  Permanent database/storage backup checksums also match.
- Preserved 11 active + 6 archived plants, 79 photos, 85 assets, 4 purchases
  (including voided records), 49 activities and 138 journal entries. Garden
  owner/workspace/entity IDs and original capture dates remain unchanged.
- On an isolated restored copy, real browser registration consumed the private
  link, cleared its URL fragment, claimed the existing garden, read every plant
  and purchase with archived/voided filters, and served a private photo.
  Saved AI connection's live test succeeded. A queued export reached READY.
  Rehearsal query helpers were corrected to use the actual items connection
  shape and explicit archived/voided filters; no application query semantics
  were changed to satisfy those helpers.
- Identity/HTTP suites pass, including protected exchange/cookie, atomic
  concurrent claim, replay/expiry rejection and recovery cancellation. All 493
  Angular tests pass; production combined image build succeeds.
- Actual production worker completed care.tick, knowledge.enrich,
  media.cover_quality, suggestions.expire and weather.fetch jobs after startup.
- All three local entry points respond ready: 4311, the preserved 8080 API/MCP
  alias, and the existing phone address on 4313. Source old runtime is stopped.
- Plan/packet/boundary and whitespace checks pass. Rehearsal containers/volumes
  were removed after successful verification; production volumes remain intact.

Permanent private deployment: /Users/dodgerbluee/dev/canopy-local/compose.yaml.
Rollback backup: that folder's backups/before-registration-2026-11-12.
Source and target runtime pointers were saved privately under .artifacts/dev.
No credentials or registration capabilities appear in this evidence.

The one-use registration page was opened in the user's browser. Final username
and password entry is intentionally left to the user; public signup on the
existing garden is closed. The protected link expires after 24 hours and may be
reopened with the private open-registration.command helper. The operator did
not choose or enter production account credentials. Registration had not yet
been submitted when the final runtime verification was recorded.

After verification, the original canopy-pg and empty 7081 preview were stopped
without deleting volumes. The active application stack now consists of the
new production app and PostgreSQL only; 4311 readiness was checked again.

Owner subsequently completed registration. Verified one active administrator,
consumed handover capability, and all 17 plant records plus 79 photos still
present before preparing v1.1.0 publication.
