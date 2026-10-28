-- One provider token per login (session family); see docs/push.md.
CREATE TABLE push_devices (
    family_id UUID PRIMARY KEY REFERENCES sessions (id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('fcm')),
    token TEXT NOT NULL UNIQUE CHECK (length(token) <= 4096 AND token ~ '^[A-Za-z0-9_:.-]+$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX push_devices_user ON push_devices (user_id, updated_at);

-- Revoking a session without replacing it ends its login (logout, revoke-all,
-- refresh reuse, credential changes); rotation sets replaced_by and keeps the
-- token. Deleting here keeps every revocation path in one transaction with
-- the token removal.
CREATE FUNCTION push_devices_end_login() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    DELETE FROM push_devices WHERE family_id = NEW.family_id;
    RETURN NULL;
END
$$;
CREATE TRIGGER sessions_end_login_push AFTER UPDATE OF revoked_at ON sessions
    FOR EACH ROW WHEN (OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL AND NEW.replaced_by IS NULL)
    EXECUTE FUNCTION push_devices_end_login();
