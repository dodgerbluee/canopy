-- name: InsertOwner :one
INSERT INTO owner (id, password_hash)
VALUES ($1, $2)
RETURNING *;

-- name: GetOwner :one
SELECT * FROM owner
ORDER BY created_at
LIMIT 1;

-- name: GetOwnerByID :one
SELECT * FROM owner
WHERE id = $1;

-- name: CountOwners :one
SELECT count(*) FROM owner;

-- name: UpdateOwnerPassword :one
UPDATE owner SET password_hash = sqlc.arg('password_hash')
WHERE id = sqlc.arg('id')
RETURNING *;

-- name: RecoverPrimaryUser :exec
WITH closed AS (UPDATE registration_handover SET consumed_at=now() WHERE registration_handover.owner_id=$1 AND consumed_at IS NULL)
UPDATE auth_users SET disabled=false,role='ADMIN' WHERE auth_users.owner_id=$1 AND is_primary;
