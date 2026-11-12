-- name: InsertSession :one
INSERT INTO sessions (id, owner_id, token_hash, csrf_token, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetSessionByTokenHash :one
SELECT * FROM sessions
WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now();

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now() WHERE id = $1;

-- name: RevokeSession :one
UPDATE sessions SET revoked_at = now()
WHERE id = sqlc.arg('id') AND owner_id = sqlc.arg('owner_id') AND revoked_at IS NULL
RETURNING *;

-- name: RevokeAllSessions :exec
UPDATE sessions SET revoked_at = now()
WHERE owner_id = $1 AND revoked_at IS NULL;
