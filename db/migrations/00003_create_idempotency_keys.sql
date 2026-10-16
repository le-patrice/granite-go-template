-- +goose Up
CREATE TABLE idempotency_keys (
    scope         text        NOT NULL,
    key           text        NOT NULL,
    request_hash  bytea       NOT NULL,
    status        integer,
    response_body bytea,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, key)
);

CREATE INDEX idempotency_keys_created_at_idx ON idempotency_keys (created_at);

-- +goose Down
DROP TABLE IF EXISTS idempotency_keys;
