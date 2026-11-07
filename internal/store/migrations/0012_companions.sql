-- A companion is a Personal session's long-lived identity: a directory with
-- a persona, and the main session that talks as it. Its name, harness and
-- archive time are the main session's, so only the link is stored here.
-- in_session_dir marks a main session that worked in its session directory
-- before companions existed; it keeps working there, where its harness's
-- native session lives.
CREATE TABLE companions (
    id             TEXT PRIMARY KEY,
    main_session   TEXT NOT NULL UNIQUE REFERENCES sessions (id),
    in_session_dir INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL
) STRICT;

-- Every Personal session so far becomes a companion's main session.
INSERT INTO companions (id, main_session, in_session_dir, created_at)
SELECT lower(hex(randomblob(8))), id, native_id != '', created_at
FROM sessions WHERE mode = 'personal';
