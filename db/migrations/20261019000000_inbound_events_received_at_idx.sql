-- +goose Up
-- Makes the retention sweep (DELETE ... WHERE received_at < cutoff) an index range scan.
CREATE INDEX IF NOT EXISTS inbound_events_received_at_idx ON inbound_events (received_at);

-- +goose Down
DROP INDEX IF EXISTS inbound_events_received_at_idx;
