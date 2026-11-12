# MIG02 — Remote production data copy

Status: ACCEPTED.

Connected to the operator-authorized SSH host. Docker access was initially denied;
the user granted the Docker group, then a fresh SSH connection verified access.
Destination had one registered account and no plants/photos/assets/purchases.
Its running application was the published socket-free 1.1.2 image.

Executed migration:

- Privately saved destination container configuration, full PostgreSQL custom
  dump, complete storage tar and fingerprints after gracefully stopping the app.
  Backup checksums were verified again after copying a rollback set locally.
- Briefly stopped the local application to capture all database schemas via
  pg_dump and the entire storage volume, including encryption/signing keys.
  Resumed the unchanged local application and verified readiness HTTP 200.
- Transferred the private snapshot over SSH. Verified archive SHA-256 values,
  restored a separate candidate database and staged storage, then matched all
  61 public-table row counts/hashes and all 410 file hashes before cutover.
- Retained the original destination database as canopy_before_mig02. Swapped the
  verified candidate into the existing database name and copied staged storage
  with permissions/ownership preserved. Existing destination public URL, DB
  role/password and container configuration remain in place.
- Verified 17 plants, 79 photos, 85 assets, 4 purchases and one registered local
  account on the healthy destination. All original IDs, photo capture dates,
  password hashes, garden records, saved AI connections/bindings and key bytes
  were part of the verified full database/storage transfer.
- Public readiness and login page returned HTTP 200. A known private photo
  returned HTTP 401 without a session, confirming its auth boundary. No user
  password was entered, changed or bypassed for verification.
- Real post-startup care.tick, knowledge.enrich, media.cover_quality and
  weather.fetch jobs completed. All 410 original files still match after startup.
- The saved AI endpoint is reachable from the application container (HTTP 401
  on a credential-free models probe). This is connectivity evidence, not an
  authenticated provider/inference test; encrypted settings and keys match source.

Source and destination snapshots, original container configuration, rollback
helper and detailed reconciliation reports remain private/ignored. No database,
media, credentials, SSH identity or operator host details were published.
The local application remains running as an independent copy, with no ongoing
replication. All migration data/configuration changes target only the authorized
Canopy deployment; the destination's original data remains recoverable.
