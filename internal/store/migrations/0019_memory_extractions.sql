-- Background extraction runs, how far each session has been read, and the
-- switch. Not derived from the files.
CREATE TABLE memory_extractions (
	id         INTEGER PRIMARY KEY,
	session_id TEXT NOT NULL,
	harness    TEXT NOT NULL,
	model      TEXT NOT NULL,
	from_seq   INTEGER NOT NULL,
	to_seq     INTEGER NOT NULL,
	trigger    TEXT NOT NULL,    -- archive | handoff | report | idle
	state      TEXT NOT NULL,    -- pending | running | parsed | done | failed
	attempts   INTEGER NOT NULL DEFAULT 0,
	output     TEXT NOT NULL DEFAULT '',
	usage      TEXT NOT NULL DEFAULT '{}', -- JSON: proto.Usage
	error      TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX memory_extractions_open ON memory_extractions (session_id)
	WHERE state IN ('pending', 'running', 'parsed');
CREATE INDEX memory_extractions_day ON memory_extractions (harness, created_at);
CREATE INDEX memory_extractions_session ON memory_extractions (session_id, id);

CREATE TABLE memory_extraction_progress (
	session_id  TEXT PRIMARY KEY,
	through_seq INTEGER NOT NULL
) STRICT;

-- Sessions recorded before extraction existed are not new evidence.
INSERT INTO memory_extraction_progress (session_id, through_seq)
	SELECT id, last_seq FROM sessions;

CREATE TABLE memory_extraction_state (
	id      INTEGER PRIMARY KEY CHECK (id = 1),
	enabled INTEGER NOT NULL
) STRICT;

INSERT INTO memory_extraction_state (id, enabled) VALUES (1, 1);
