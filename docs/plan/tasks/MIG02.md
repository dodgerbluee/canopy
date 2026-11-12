# MIG02 — Remote migration

Status: ACCEPTED.

### MIG02 — Remote production data migration

Depends: MIG01, DEP03.

Own: `docs/plan/tasks/MIG02.md`, `.artifacts/agents/MIG02/`, `.artifacts/dev/migration/`, `docs/deployment.md`. Integrator owns graph/ledger. Authorized operator scope: SSH to the user-authorized destination using the supplied identity; inspect, back up and migrate into containers canopy/canopy-db. Privately preserve remote configuration and existing data before replacement. Keep local source and rollback assets.

Acceptance: Transfer complete database and media/encryption-key storage consistently; retain garden IDs, users/passwords, acquisitions, care, knowledge, AI and agent records. Preserve target connection/public URL settings and verify counts, row fingerprints, file hashes, readiness and worker processing. Do not overwrite an unrelated garden without clarification. Do not disclose credentials or publish private backups.

Verify: Source/destination inventories and backups with checksums, quiesced final snapshot, restored table and file reconciliation, actual target readiness/media and worker verification. Keep both source and destination rollback backups.

Full logical PostgreSQL and complete storage migration verified. All 61 public
tables matched before startup; all 410 transferred files matched after startup.
The destination retains its image, connection credentials and public URL. Source
account/password hashes, garden IDs, AI settings and encryption keys are intact.
Both source and original destination rollback snapshots are retained privately.
The original destination database remains available as canopy_before_mig02.
Local resumed after snapshot; the two running instances are independent copies.
Evidence: `.artifacts/agents/MIG02/EVIDENCE.md`.
