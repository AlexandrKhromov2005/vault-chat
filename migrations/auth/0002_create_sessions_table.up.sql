CREATE TABLE IF NOT EXISTS sessions (
    id          UUID PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Id of the login session this one descends from by refresh token
    -- rotation; equals id for the session created at login.
    family_id   UUID        NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    -- Set when the session is revoked by refresh token rotation; tells token
    -- reuse (theft) apart from presenting a logged-out token.
    replaced_by UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT sessions_replaced_only_if_revoked CHECK (replaced_by IS NULL OR revoked_at IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions (user_id);
CREATE INDEX IF NOT EXISTS sessions_family_id_idx ON sessions (family_id);
