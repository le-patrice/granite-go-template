-- name: InsertRefreshToken :one
INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at)
VALUES (sqlc.arg(user_id), sqlc.arg(family_id), sqlc.arg(token_hash), sqlc.arg(expires_at))
RETURNING *;

-- name: GetRefreshTokenByHashForUpdate :one
SELECT * FROM refresh_tokens
WHERE token_hash = sqlc.arg(token_hash)
FOR UPDATE;

-- name: MarkRefreshTokenRotated :execrows
UPDATE refresh_tokens
SET rotated_at = now()
WHERE id = sqlc.arg(id)
  AND rotated_at IS NULL
  AND revoked_at IS NULL;

-- name: RevokeRefreshTokenFamily :execrows
UPDATE refresh_tokens
SET revoked_at = now()
WHERE family_id = sqlc.arg(family_id)
  AND revoked_at IS NULL;

-- name: DeleteExpiredRefreshTokens :execrows
DELETE FROM refresh_tokens
WHERE expires_at < sqlc.arg(cutoff);
