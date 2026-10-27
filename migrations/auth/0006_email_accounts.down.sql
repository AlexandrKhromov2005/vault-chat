DROP TABLE IF EXISTS email_outbox;
DROP TABLE IF EXISTS email_actions;
ALTER TABLE sessions DROP COLUMN credential_version;
ALTER TABLE users DROP COLUMN credential_version;
ALTER TABLE users DROP COLUMN email_verification_required;
ALTER TABLE users DROP COLUMN email_verified_at;
