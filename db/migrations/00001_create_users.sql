-- +goose Up
CREATE TABLE users (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text        NOT NULL,
    password_hash text        NOT NULL,
    role          text        NOT NULL CHECK (role IN ('admin', 'member')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    disabled_at   timestamptz
);

CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));

-- +goose Down
DROP TABLE IF EXISTS users;
