-- name: CreateUser :one
INSERT INTO users (email, password_hash, role)
VALUES (sqlc.arg(email), sqlc.arg(password_hash), sqlc.arg(role))
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = sqlc.arg(id);

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE lower(email) = lower(sqlc.arg(email));

-- name: CountAdmins :one
SELECT count(*) FROM users
WHERE role = 'admin';

-- name: UpdateUserPasswordHash :exec
UPDATE users
SET password_hash = sqlc.arg(password_hash), updated_at = now()
WHERE id = sqlc.arg(id);
