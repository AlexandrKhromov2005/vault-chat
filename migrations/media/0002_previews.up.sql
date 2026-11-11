CREATE TABLE preview_jobs (
 file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
 preset TEXT NOT NULL CHECK (preset = 'p320'),
 decoder_version TEXT NOT NULL CHECK (octet_length(decoder_version) BETWEEN 1 AND 64),
 state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','running','ready','failed','skipped')),
 attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
 infrastructure_retries INTEGER NOT NULL DEFAULT 0 CHECK (infrastructure_retries >= 0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 lease_owner TEXT,
 lease_until TIMESTAMPTZ,
 last_error TEXT CHECK (last_error IS NULL OR octet_length(last_error) <= 512),
 PRIMARY KEY (file_id, preset, decoder_version)
);
CREATE INDEX preview_jobs_claim_idx ON preview_jobs (next_attempt_at, lease_until)
 WHERE state IN ('pending', 'running');

CREATE TABLE preview_objects (
 key TEXT PRIMARY KEY,
 file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
 state TEXT NOT NULL DEFAULT 'uploading' CHECK (state IN ('uploading','ready','replaced','collecting')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 replaced_at TIMESTAMPTZ
);
CREATE INDEX preview_objects_gc_idx ON preview_objects (state, created_at, replaced_at);

CREATE TABLE file_previews (
 file_id UUID NOT NULL REFERENCES files(id) ON DELETE CASCADE,
 preset TEXT NOT NULL CHECK (preset = 'p320'),
 key TEXT NOT NULL UNIQUE REFERENCES preview_objects(key),
 decoder_version TEXT NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY (file_id, preset)
);
