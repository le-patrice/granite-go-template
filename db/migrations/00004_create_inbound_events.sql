-- +goose Up
CREATE TABLE inbound_events (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source      text        NOT NULL,
    external_id text        NOT NULL,
    payload     jsonb       NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, external_id)
);

-- +goose Down
DROP TABLE IF EXISTS inbound_events;
