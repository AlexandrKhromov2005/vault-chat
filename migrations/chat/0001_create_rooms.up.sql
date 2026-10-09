-- Apply only to Chat's own database. Auth user IDs are external references.
CREATE TABLE rooms (
    id UUID PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('direct', 'channel')),
    name TEXT,
    owner_id UUID,
    private BOOLEAN NOT NULL,
    direct_user_low UUID,
    direct_user_high UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT rooms_shape CHECK (
        (kind = 'direct' AND name IS NULL AND owner_id IS NULL AND private
         AND direct_user_low IS NOT NULL AND direct_user_high IS NOT NULL
         AND direct_user_low <> '00000000-0000-0000-0000-000000000000'::uuid
         AND direct_user_low < direct_user_high)
        OR
        (kind = 'channel' AND name IS NOT NULL AND char_length(name) BETWEEN 1 AND 80
         AND owner_id IS NOT NULL AND owner_id <> '00000000-0000-0000-0000-000000000000'::uuid
         AND direct_user_low IS NULL AND direct_user_high IS NULL)
    ),
    CONSTRAINT rooms_direct_pair UNIQUE (direct_user_low, direct_user_high)
);

CREATE TABLE room_members (
    room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    user_id UUID NOT NULL CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id)
);

CREATE INDEX room_members_user_id_room_id_idx ON room_members (user_id, room_id);
