# Portability runbook (T38.a — R1 scope)

Operator procedures for export, backup and restore. Everything here runs
through the `admin` CLI (`server/cmd/admin`) against the stack named by
`DATABASE_URL`. R1 scope: the R1 core schema only. Care/insight table
coverage and configured backup destinations land with T38.final.

## Concepts

- **Backup set** — one run writes three files into `--dest`:
  - `<set>.tar` (or `<set>.tar.enc` when sealed) — every scoped table as
    JSONL (`db/<table>.jsonl`), river jobs (`river/river_job.jsonl`) and
    every referenced media object (`media/<storage-key>`).
  - `<set>.ledger` (or `.ledger.enc`) — the deletion ledger: tombstones,
    privacy-epoch watermark and gone-marked idempotency receipts. It is
    kept **outside** the archive on purpose: erasing a plant must stay
    visible even if an older archive is restored.
  - `<set>.manifest.json` — plaintext, per-entry SHA-256s plus
    `payloadSha256` over the archive file exactly as stored. Schema:
    `manifest.schema.json`.
- **Sealing** — `--key-file` points at a file containing 64 hex chars
  (AES-256-GCM, magic `CANOPYBAK1`). Generate with
  `openssl rand -hex 32 > backup.key && chmod 600 backup.key`.
  The key lives **outside** the backup destination. Lose the key and the
  set is unrecoverable; there is no recovery path and no key in code.
- **Quiesce** — a backup sets the maintenance flag (mutations and uploads
  answer 503, reads keep working), pauses all river queues (`*`) and
  waits for running jobs to drain before taking the snapshot. The stack
  is **always** reopened afterwards, including on failure.
- **Restore is blank-host only** — it refuses any database where the app
  schema already exists (`to_regclass('plants')`). It never merges.
  Sessions are revoked on restore; export archives are not part of a
  set (re-request after restore).

## Backup

```sh
cd server
DATABASE_URL=postgres://… CANOPY_STORAGE_ROOT=/var/lib/canopy/storage \
  go run ./cmd/admin backup [--dest DIR] [--key-file PATH]
```

- Default dest is `$CANOPY_STORAGE_ROOT/backups`.
- A referenced media object missing from the store fails the backup
  honestly (never silently skips) — and the stack is reopened anyway.
- Verify the set before shipping it: the manifest's `payloadSha256`
  must match `shasum -a 256 <set>.tar[.enc]`.

## Restore (blank host)

1. Provision an **empty** database (or an empty schema via
   `search_path`) — no migrations, no data. Restore applies goose
   migrations and the river migration itself.
2. Point a fresh storage root at where media files should land.
3. Run:

```sh
cd server
DATABASE_URL=postgres://… CANOPY_STORAGE_ROOT=/var/lib/canopy/storage \
  go run ./cmd/admin restore --archive SET.tar[.enc] [--ledger SET.ledger[.enc]] [--key-file PATH]
```

- The external `<set>.manifest.json` must sit beside the archive.
- Sealed set without `--key-file` → refused before anything is written.
- Tampered archive or entry → checksum refusal before any write.
- On success the printed summary is the **reconciliation report**:
  table rows, media files hash-verified against the manifest, ledger
  rows re-applied, financial totals (live purchase totals per currency
  and allocation sums must equal the manifest's), and measured restore
  duration. Any mismatch fails the restore, it never reports green on
  drift.
4. Start the stack. All previous sessions are invalid; users sign in
   again. Request fresh exports if needed.

`make restore-test ARCHIVE=.artifacts/backups/<set>.tar` runs the whole
blank-schema round trip against the disposable dev container and drops
the scratch schema afterwards.

## User exports

- Users request an export via the `requestExport` mutation; at most one
  QUEUED/RUNNING/READY export exists per workspace (repeat requests
  return the live job). The worker builds `<storageRoot>/exports/<id>.tar`
  — CSV/JSONL datasets + media + an in-archive `ExportManifest` naming
  what was excluded (password hashes, sessions, CSRF, receipts, audit
  priors, other workspaces).
- Download: `GET /api/exports/{id}/download` (owner-scoped; 202 while
  building, 410 once failed/expired, ETag from the manifest sha).
- Archives expire 24h after build; expiry schedules file cleanup.
- **Purge fencing**: purging a plant bumps the workspace privacy epoch
  and revokes live exports; a revoked export never publishes bytes.

## Plant purge semantics (deletion matrix)

- Journal entries, activities, measurements, contexts, acquisitions,
  activities cascade; the plant row is hard-deleted.
- Purchase allocations keep their money: `plant_id` becomes NULL (a
  deleted-plant marker) so financial totals never change.
- Shared receipt assets survive any single plant purge; only
  unreferenced assets move to DELETING with a scheduled purge job.
- Every purge writes tombstones + marks matching idempotency receipts
  gone, so replays answer RESOURCE_GONE forever.

## Known limits (R1)

- Backup covers R1 core tables only (care/insight arrive with
  T38.final); sessions and export archives are deliberately excluded.
- No scheduled/remote backup destinations yet — that is operator
  infrastructure (T39); this lane guarantees the set is portable,
  verifiable and restorable.
