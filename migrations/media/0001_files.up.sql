CREATE TABLE files (
 id UUID PRIMARY KEY,
 room_id UUID NOT NULL,
 owner_id UUID NOT NULL,
 name TEXT NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 255),
 content_type TEXT NOT NULL,
 size BIGINT NOT NULL CHECK (size BETWEEN 1 AND 10485760),
 created_at TIMESTAMPTZ NOT NULL,
 ready BOOLEAN NOT NULL DEFAULT false
);
CREATE INDEX files_pending_idx ON files (created_at) WHERE NOT ready;
