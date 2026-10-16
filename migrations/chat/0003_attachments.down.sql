-- Rollback refuses attachment-only messages rather than silently deleting them.
ALTER TABLE messages DROP CONSTRAINT messages_text_check;
ALTER TABLE messages ADD CONSTRAINT messages_text_check CHECK (char_length(text) BETWEEN 1 AND 4096);
ALTER TABLE messages DROP COLUMN attachment_ids;
