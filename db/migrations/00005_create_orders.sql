-- template:example:file orders
-- +goose Up
-- This migration belongs to the removable "orders" example.
CREATE TABLE orders (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_id     uuid        NOT NULL REFERENCES users (id),
    reference    text        NOT NULL,
    amount_minor bigint      NOT NULL CHECK (amount_minor > 0),
    currency     char(3)     NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_id, reference)
);

CREATE INDEX orders_owner_id_id_idx ON orders (owner_id, id);

-- +goose Down
DROP TABLE IF EXISTS orders;
