CREATE TABLE registration_invitations (
    token_hash BYTEA PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    email CITEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX registration_invitations_expiry ON registration_invitations (expires_at);
