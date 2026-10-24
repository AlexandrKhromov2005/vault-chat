-- Do not rename existing users automatically. Case-colliding names make this
-- migration fail atomically until the operator resolves their ownership.
CREATE UNIQUE INDEX users_username_casefold_unique ON users (lower(username));
DROP INDEX users_username_unique;
