-- artifacts holds each session's artifacts: a name is unique within its
-- session, and every version of an artifact has the same kind.
CREATE TABLE artifacts (
    id         TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions (id),
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (session_id, name)
) STRICT;

-- artifact_versions holds every published version; a version never
-- changes. Its files live under artifacts/<session>/<id>/v<version>/.
CREATE TABLE artifact_versions (
    artifact_id TEXT NOT NULL REFERENCES artifacts (id),
    version     INTEGER NOT NULL,
    entry       TEXT NOT NULL,
    by          TEXT NOT NULL,
    files       INTEGER NOT NULL,
    size        INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (artifact_id, version)
) STRICT, WITHOUT ROWID;
