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
