ALTER TABLE messages ADD COLUMN attachment_ids UUID[] NOT NULL DEFAULT '{}';
ALTER TABLE messages DROP CONSTRAINT messages_text_check;
ALTER TABLE messages ADD CONSTRAINT messages_text_check CHECK (
 char_length(text) <= 4096 AND (char_length(text) > 0 OR cardinality(attachment_ids) > 0)
);
ALTER TABLE messages ADD CONSTRAINT messages_attachments_check CHECK (
 cardinality(attachment_ids) <= 5 AND array_position(attachment_ids, NULL) IS NULL
);
