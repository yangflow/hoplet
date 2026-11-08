-- Every write to a memory entry, in order. Audit rows are never rebuilt.
CREATE TABLE memory_ops (
	id               TEXT PRIMARY KEY,
	entry_id         TEXT NOT NULL,
	expected_version INTEGER NOT NULL,
	new_version      INTEGER NOT NULL,
	actor            TEXT NOT NULL,    -- user | companion | extraction | file
	reason           TEXT NOT NULL,
	content          TEXT NOT NULL,    -- the whole entry file
	hash             TEXT NOT NULL,    -- SHA-256 of content
	state            TEXT NOT NULL,    -- pending | committed | failed
	created_at       INTEGER NOT NULL,
	finished_at      INTEGER
) STRICT;

CREATE UNIQUE INDEX memory_ops_committed ON memory_ops (entry_id, new_version) WHERE state = 'committed';
CREATE INDEX memory_ops_pending ON memory_ops (state) WHERE state = 'pending';

-- Derived from the entry files; a scan can rebuild it.
CREATE TABLE memory_index (
	id          TEXT PRIMARY KEY,
	version     INTEGER NOT NULL,
	category    TEXT NOT NULL,
	status      TEXT NOT NULL,
	scope_kind  TEXT NOT NULL,
	scope_value TEXT NOT NULL,
	title       TEXT NOT NULL,
	body        TEXT NOT NULL,
	hash        TEXT NOT NULL,
	updated_at  INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

-- Entry files the last scan could not use.
CREATE TABLE memory_problems (
	path TEXT PRIMARY KEY,
	err  TEXT NOT NULL
) STRICT, WITHOUT ROWID;
