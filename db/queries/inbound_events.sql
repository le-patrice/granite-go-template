-- name: InsertInboundEvent :one
INSERT INTO inbound_events (source, external_id, payload)
VALUES (sqlc.arg(source), sqlc.arg(external_id), sqlc.arg(payload))
ON CONFLICT (source, external_id) DO NOTHING
RETURNING id;

-- name: GetInboundEvent :one
SELECT * FROM inbound_events
WHERE id = sqlc.arg(id);
