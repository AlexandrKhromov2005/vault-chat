ALTER TABLE rooms ADD COLUMN message_sequence BIGINT NOT NULL DEFAULT 0 CHECK (message_sequence >= 0);

CREATE TABLE messages (
    id UUID PRIMARY KEY,
    room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    sender_id UUID NOT NULL CHECK (sender_id <> '00000000-0000-0000-0000-000000000000'),
    client_id UUID NOT NULL CHECK (client_id <> '00000000-0000-0000-0000-000000000000'),
    text TEXT NOT NULL CHECK (char_length(text) BETWEEN 1 AND 4096),
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (room_id, sender_id, client_id),
    UNIQUE (room_id, sequence)
);
