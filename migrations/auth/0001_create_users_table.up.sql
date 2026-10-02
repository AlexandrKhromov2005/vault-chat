CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE IF NOT EXISTS users (
    id            UUID PRIMARY KEY,
    email         CITEXT      NOT NULL,
    username      VARCHAR(32) NOT NULL,
    password_hash TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS users_email_unique ON users (email);
CREATE UNIQUE INDEX IF NOT EXISTS users_username_unique ON users (username);
