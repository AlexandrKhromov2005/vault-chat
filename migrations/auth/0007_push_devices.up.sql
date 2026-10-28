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

