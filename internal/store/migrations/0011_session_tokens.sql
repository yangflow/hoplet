-- A session's tool token, kept only as its SHA-256. One per session; a new
-- token replaces the old one, and archiving the session removes it.
CREATE TABLE session_tokens (
    session_id TEXT PRIMARY KEY REFERENCES sessions (id),
    hash       BLOB NOT NULL UNIQUE,
    issued_at  INTEGER NOT NULL
) STRICT;
