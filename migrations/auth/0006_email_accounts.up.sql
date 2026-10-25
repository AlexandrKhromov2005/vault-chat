ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;
-- Existing users retain an explicit legacy transition, never a verified claim.
ALTER TABLE users ADD COLUMN email_verification_required BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN credential_version BIGINT NOT NULL DEFAULT 0 CHECK (credential_version >= 0);
ALTER TABLE sessions ADD COLUMN credential_version BIGINT NOT NULL DEFAULT 0 CHECK (credential_version >= 0);

CREATE TABLE email_actions (
    id UUID PRIMARY KEY,
    token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash)=32),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN ('verify','reset','change')),
    account_email CITEXT NOT NULL,
    target_email CITEXT NOT NULL,
    credential_version BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX email_actions_user_purpose_created ON email_actions(user_id,purpose,created_at);
CREATE INDEX email_actions_expiry ON email_actions(expires_at);

CREATE TABLE email_outbox (
    id UUID PRIMARY KEY,
    action_id UUID REFERENCES email_actions(id) ON DELETE CASCADE,
    payload BYTEA NOT NULL CHECK (octet_length(payload) BETWEEN 29 AND 65565),
    expires_at TIMESTAMPTZ NOT NULL,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts>=0),
    lease UUID,
    lease_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX email_outbox_ready ON email_outbox(available_at,created_at);
