-- name: ReserveIdempotencyKey :execrows
INSERT INTO idempotency_keys (scope, key, request_hash)
VALUES (sqlc.arg(scope), sqlc.arg(key), sqlc.arg(request_hash))
ON CONFLICT (scope, key) DO NOTHING;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys
WHERE scope = sqlc.arg(scope) AND key = sqlc.arg(key);

-- name: CompleteIdempotencyKey :exec
UPDATE idempotency_keys
SET status = sqlc.arg(status), response_body = sqlc.arg(response_body)
WHERE scope = sqlc.arg(scope) AND key = sqlc.arg(key);

-- name: DeleteIdempotencyKey :exec
DELETE FROM idempotency_keys
WHERE scope = sqlc.arg(scope) AND key = sqlc.arg(key);

-- name: DeleteExpiredIdempotencyKeys :execrows
DELETE FROM idempotency_keys
WHERE created_at < sqlc.arg(cutoff);
