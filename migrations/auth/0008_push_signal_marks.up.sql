-- Recipients of a stored push signal whose push was handled, so that a
-- redelivery skips them (docs/adr/0003-push-signals-jetstream.md). Signal
-- ids are opaque hashes; there is no foreign key to users, since a signal
-- may name ids Auth does not know. Rows expire after two hours and are
-- deleted in batches.
CREATE TABLE push_signal_marks (
    signal_id TEXT NOT NULL CHECK (signal_id ~ '^[0-9a-f]{64}$'),
    user_id UUID NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (signal_id, user_id)
);
CREATE INDEX push_signal_marks_expiry ON push_signal_marks (expires_at);
