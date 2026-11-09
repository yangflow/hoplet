-- Link changes commit like content changes but keep the entry's version.
ALTER TABLE memory_ops ADD COLUMN kind TEXT NOT NULL DEFAULT 'content'; -- content | links
DROP INDEX memory_ops_committed;
CREATE UNIQUE INDEX memory_ops_committed ON memory_ops (entry_id, new_version)
	WHERE state = 'committed' AND kind = 'content';

-- The memory settings, one row.
CREATE TABLE memory_settings (
	id                 INTEGER PRIMARY KEY CHECK (id = 1),
	use_personal       INTEGER NOT NULL,
	use_code           INTEGER NOT NULL,
	extraction         INTEGER NOT NULL,
	extraction_harness TEXT NOT NULL,  -- empty follows each session
	include_code       INTEGER NOT NULL,
	auto_save          INTEGER NOT NULL
) STRICT;

INSERT INTO memory_settings (id, use_personal, use_code, extraction, extraction_harness, include_code, auto_save)
	SELECT 1, 1, 1, enabled, '', 0, 1 FROM memory_extraction_state;

DROP TABLE memory_extraction_state;

-- Which entries each session really had: whole in its digest, or read. Not
-- derived from the files.
CREATE TABLE memory_uses (
	id         INTEGER PRIMARY KEY,
	session_id TEXT NOT NULL,
	entry_id   TEXT NOT NULL,
	version    INTEGER NOT NULL,
	way        TEXT NOT NULL,    -- injected | read
	created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX memory_uses_entry ON memory_uses (entry_id, session_id);
CREATE INDEX memory_uses_session ON memory_uses (session_id, id);

INSERT INTO memory_uses (session_id, entry_id, version, way, created_at)
	SELECT i.session_id, json_extract(e.value, '$.id'), json_extract(e.value, '$.version'), 'injected', i.created_at
	FROM memory_injections i, json_each(i.entries) e
	WHERE i.status = 'delivered' AND NOT coalesce(json_extract(e.value, '$.indexed'), 0);
