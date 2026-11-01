CREATE TABLE baselines (
    session_id TEXT NOT NULL REFERENCES sessions(id),
    taken_at   INTEGER NOT NULL,
    error      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id)
) STRICT, WITHOUT ROWID;

CREATE TABLE baseline_repos (
    session_id TEXT NOT NULL REFERENCES sessions(id),
    repo       TEXT NOT NULL,
    tree       TEXT NOT NULL,
    skipped    TEXT NOT NULL DEFAULT '[]',
    error      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, repo)
) STRICT, WITHOUT ROWID;
