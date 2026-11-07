CREATE TABLE devices (
    id         TEXT PRIMARY KEY,
    public_key BLOB NOT NULL,
    name       TEXT NOT NULL,
    paired_at  INTEGER NOT NULL,
    revoked_at INTEGER
) STRICT;

CREATE TABLE hosts (
    id         TEXT PRIMARY KEY,
    public_key BLOB NOT NULL,
    name       TEXT NOT NULL UNIQUE,
    routes     TEXT NOT NULL,
    added_at   INTEGER NOT NULL
) STRICT;
