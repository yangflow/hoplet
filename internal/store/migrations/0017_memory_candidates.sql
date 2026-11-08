-- What each candidate entry proposes. Not derived from the files.
CREATE TABLE memory_candidates (
	entry_id         TEXT PRIMARY KEY,
	action           TEXT NOT NULL,    -- create | update | withdraw
	target_id        TEXT NOT NULL,    -- empty for create
	expected_version INTEGER NOT NULL,
	actor            TEXT NOT NULL,
	reason           TEXT NOT NULL,
	state            TEXT NOT NULL,    -- open | accepted | rejected
	created_at       INTEGER NOT NULL,
	decided_at       INTEGER
) STRICT, WITHOUT ROWID;

CREATE INDEX memory_candidates_open ON memory_candidates (created_at) WHERE state = 'open';
