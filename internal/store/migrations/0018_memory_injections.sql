-- Which memory entries each started session was offered. Not derived from
-- the files.
CREATE TABLE memory_injections (
	id         INTEGER PRIMARY KEY,
	session_id TEXT NOT NULL,
	status     TEXT NOT NULL,    -- delivered | failed | unsupported
	entries    TEXT NOT NULL,    -- JSON: [{id, version, indexed}]
	detail     TEXT NOT NULL,
	created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX memory_injections_session ON memory_injections (session_id, id);
