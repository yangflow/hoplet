-- parent is the session that created this one through the tool bridge: a
-- delegated Personal session or a handed-off Code session. It is set when
-- the session is created and never changes.
ALTER TABLE sessions ADD COLUMN parent TEXT NOT NULL DEFAULT '';

-- from_session is the session whose report a queued message is.
ALTER TABLE queue ADD COLUMN from_session TEXT NOT NULL DEFAULT '';

-- reports holds every report key a session was given, so a key reaches the
-- queue once even after the queued message is sent or deleted.
CREATE TABLE reports (
    parent     TEXT NOT NULL REFERENCES sessions (id),
    key        TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (parent, key)
) STRICT, WITHOUT ROWID;
