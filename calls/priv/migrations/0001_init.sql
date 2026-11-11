CREATE TABLE IF NOT EXISTS calls (
    id text PRIMARY KEY,
    room_id text NOT NULL,
    state text NOT NULL DEFAULT 'ringing' CHECK (state IN ('ringing', 'connecting', 'active', 'ended')),
    state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
    caller_id text NOT NULL,
    caller_device text NOT NULL,
    callee_id text NOT NULL,
    callee_device text,
    connected_caller boolean NOT NULL DEFAULT false,
    connected_callee boolean NOT NULL DEFAULT false,
    offered boolean NOT NULL DEFAULT false,
    pending_offer text CHECK (pending_offer IN ('caller', 'callee')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    state_entered_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    caller_heartbeat_at timestamptz,
    callee_heartbeat_at timestamptz,
    ended_at timestamptz,
    CHECK (caller_id <> callee_id)
);

CREATE TABLE IF NOT EXISTS call_participants (
    user_id text PRIMARY KEY,
    call_id text NOT NULL REFERENCES calls(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS call_capacity (
    id integer PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    active_calls integer NOT NULL DEFAULT 0 CHECK (active_calls >= 0),
    capacity_limit integer NOT NULL DEFAULT 200 CHECK (capacity_limit BETWEEN 0 AND 200),
    CHECK (active_calls <= capacity_limit)
);
INSERT INTO call_capacity (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS call_commands (
    device text NOT NULL,
    command_id text NOT NULL,
    call_id text NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    fingerprint bytea NOT NULL CHECK (length(fingerprint) = 32),
    response jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (device, command_id)
);
CREATE INDEX IF NOT EXISTS call_commands_created_at ON call_commands(created_at);

CREATE TABLE IF NOT EXISTS call_signals (
    call_id text NOT NULL REFERENCES calls(id) ON DELETE CASCADE,
    sender_device text NOT NULL,
    sender_seq bigint NOT NULL CHECK (sender_seq > 0),
    signal_seq bigint NOT NULL CHECK (signal_seq > 0),
    kind text NOT NULL CHECK (kind IN ('offer', 'answer', 'candidate', 'end-of-candidates')),
    payload text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (call_id, sender_device, sender_seq),
    UNIQUE (call_id, signal_seq)
);
CREATE INDEX IF NOT EXISTS call_signals_catch_up ON call_signals(call_id, signal_seq);
CREATE INDEX IF NOT EXISTS call_signals_created_at ON call_signals(created_at);
