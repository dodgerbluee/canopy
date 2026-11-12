-- name: InsertSession :one
INSERT INTO sessions (id, owner_id, token_hash, csrf_token, expires_at, user_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetSessionByTokenHash :one
SELECT s.* FROM sessions s
WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
AND NOT EXISTS (SELECT 1 FROM auth_users u
  WHERE u.id=coalesce(s.user_id,s.owner_id) AND u.disabled);

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now() WHERE id = $1;

-- name: RevokeSession :one
UPDATE sessions SET revoked_at = now()
WHERE id = sqlc.arg('id') AND owner_id = sqlc.arg('owner_id') AND revoked_at IS NULL
RETURNING *;

-- name: RevokeAllSessions :exec
UPDATE sessions SET revoked_at = now()
WHERE owner_id = $1 AND revoked_at IS NULL;
