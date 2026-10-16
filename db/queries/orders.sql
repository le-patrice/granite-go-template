-- template:example:file orders
-- name: CreateOrder :one
INSERT INTO orders (owner_id, reference, amount_minor, currency)
VALUES (sqlc.arg(owner_id), sqlc.arg(reference), sqlc.arg(amount_minor), sqlc.arg(currency))
RETURNING *;

-- name: GetOrder :one
SELECT * FROM orders
WHERE id = sqlc.arg(id);

-- name: ListOrdersByOwner :many
SELECT * FROM orders
WHERE owner_id = sqlc.arg(owner_id) AND id < sqlc.arg(cursor)
ORDER BY id DESC
LIMIT sqlc.arg(page_size)::bigint;

-- name: ListAllOrders :many
SELECT * FROM orders
WHERE id < sqlc.arg(cursor)
ORDER BY id DESC
LIMIT sqlc.arg(page_size)::bigint;
