-- Supports case-insensitive username prefix search: LIKE 'prefix%' can use a
-- btree only under the C collation, which also gives byte order for ORDER BY.
CREATE INDEX IF NOT EXISTS users_username_lower_idx ON users ((lower(username)) COLLATE "C");
