CREATE TABLE sessions (
    id              TEXT PRIMARY KEY,
    mode            TEXT NOT NULL,
    harness_kind    TEXT NOT NULL,
    harness_version TEXT NOT NULL DEFAULT '',
    native_id       TEXT NOT NULL DEFAULT '',
    directory       TEXT NOT NULL DEFAULT '',
    isolation       TEXT NOT NULL DEFAULT '',
    owner           TEXT NOT NULL,
    origin          TEXT NOT NULL,
    status          TEXT NOT NULL,
    title           TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    archived_at     INTEGER,
    last_seq        INTEGER NOT NULL DEFAULT 0,
    version         INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_version ON sessions (version);

CREATE TABLE catalog (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL
) STRICT;

INSERT INTO catalog (id, version) VALUES (1, 0);

CREATE TABLE events (
    session_id TEXT NOT NULL REFERENCES sessions(id),
    seq        INTEGER NOT NULL,
    ts         INTEGER NOT NULL,
    turn       INTEGER NOT NULL DEFAULT 0,
    type       TEXT NOT NULL,
    v          INTEGER NOT NULL,
    payload    TEXT NOT NULL,
    raw_sha256 TEXT,
    raw_size   INTEGER,
    message_id TEXT,
    output_sha256 TEXT,
    PRIMARY KEY (session_id, seq)
) STRICT;

CREATE INDEX events_message ON events (session_id, message_id) WHERE message_id IS NOT NULL;
CREATE INDEX events_raw ON events (raw_sha256) WHERE raw_sha256 IS NOT NULL;
CREATE INDEX events_output ON events (output_sha256) WHERE output_sha256 IS NOT NULL;

CREATE TABLE turns (
    session_id TEXT NOT NULL REFERENCES sessions(id),
    n          INTEGER NOT NULL,
    start_seq  INTEGER NOT NULL,
    end_seq    INTEGER,
    reason     TEXT,
    PRIMARY KEY (session_id, n)
) STRICT, WITHOUT ROWID;

CREATE TABLE approvals (
    session_id   TEXT NOT NULL REFERENCES sessions(id),
    id           TEXT NOT NULL,
    request_seq  INTEGER NOT NULL,
    kind         TEXT NOT NULL,
    status       TEXT NOT NULL,
    option_id    TEXT NOT NULL DEFAULT '',
    decided_by   TEXT NOT NULL DEFAULT '',
    resolved_seq INTEGER,
    PRIMARY KEY (session_id, id)
) STRICT, WITHOUT ROWID;

CREATE INDEX approvals_pending ON approvals (session_id) WHERE status = 'pending';

CREATE TABLE receipts (
    session_id TEXT NOT NULL REFERENCES sessions(id),
    key        TEXT NOT NULL,
    first_seq  INTEGER NOT NULL,
    last_seq   INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (session_id, key)
) STRICT, WITHOUT ROWID;

CREATE INDEX receipts_created ON receipts (created_at);

CREATE TABLE ingest_offsets (
    session_id TEXT PRIMARY KEY REFERENCES sessions(id),
    offset     INTEGER NOT NULL
) STRICT;
