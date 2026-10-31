CREATE TABLE queue (
    session_id TEXT NOT NULL REFERENCES sessions(id),
    key        TEXT NOT NULL,
    text       TEXT NOT NULL,
    position   INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    error      TEXT NOT NULL DEFAULT '',
    attempt    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (session_id, key)
) STRICT, WITHOUT ROWID;
