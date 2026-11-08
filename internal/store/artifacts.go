package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/yangflow/hoplet/proto"
)

// PublishArtifact records v as a new version of its session's artifact
// named v.Name and appends an ArtifactPublished event to the session's log
// at its open turn, or turn 0 when none is open. For the first version
// v.ID becomes the artifact's ID; later versions must name the same ID and
// kind and be the next version number. v.CreatedAt and v.UpdatedAt are
// ignored: the version is stamped now. The write is durable.
func (s *Store) PublishArtifact(ctx context.Context, v proto.Artifact) (proto.Artifact, error) {
	if v.ID == "" || v.Name == "" || v.Entry == "" || v.Version < 1 {
		return proto.Artifact{}, fmt.Errorf("%w: an artifact version needs an ID, a name, an entry and a version", ErrInvalid)
	}
	if !v.Kind.Valid() || !v.By.Valid() {
		return proto.Artifact{}, fmt.Errorf("%w: artifact kind %q or source %q", ErrInvalid, v.Kind, v.By)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	var out proto.Artifact
	err := s.tx(ctx, true, func(tx *sql.Tx) error {
		var archived sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT archived_at FROM sessions WHERE id = ?`, v.Session).Scan(&archived)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: session %s", ErrNotFound, v.Session)
		}
		if err != nil {
			return fmt.Errorf("store: read session %s: %w", v.Session, err)
		}
		if archived.Valid {
			return fmt.Errorf("%w: session %s is archived", ErrInvalid, v.Session)
		}
		cur, err := latestArtifact(ctx, tx, `a.session_id = ? AND a.name = ?`, v.Session, v.Name)
		switch {
		case errors.Is(err, ErrNotFound):
			if v.Version != 1 {
				return fmt.Errorf("%w: artifact %q has no versions yet", ErrConflict, v.Name)
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO artifacts (id, session_id, name, kind, created_at) VALUES (?, ?, ?, ?, ?)`,
				v.ID, v.Session, v.Name, v.Kind, ms(now))
			if isConstraint(err) {
				return fmt.Errorf("%w: artifact %s", ErrExists, v.ID)
			}
			if err != nil {
				return fmt.Errorf("store: create artifact: %w", err)
			}
		case err != nil:
			return err
		case cur.ID != v.ID:
			return fmt.Errorf("%w: artifact %q is %s", ErrConflict, v.Name, cur.ID)
		case cur.Kind != v.Kind:
			return fmt.Errorf("%w: artifact %q is %s, not %s", ErrInvalid, v.Name, cur.Kind, v.Kind)
		case cur.Version+1 != v.Version:
			return fmt.Errorf("%w: artifact %q is at version %d", ErrConflict, v.Name, cur.Version)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO artifact_versions (artifact_id, version, entry, by, files, size, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, v.ID, v.Version, v.Entry, v.By, v.Files, v.Size, ms(now))
		if err != nil {
			return fmt.Errorf("store: record artifact version: %w", err)
		}
		var turn uint32
		err = tx.QueryRowContext(ctx, `SELECT n FROM turns WHERE session_id = ? AND end_seq IS NULL ORDER BY n DESC LIMIT 1`,
			v.Session).Scan(&turn)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: read open turn: %w", err)
		}
		req := AppendRequest{Session: v.Session, Turn: turn, Time: now, Entries: []Entry{{Payload: proto.ArtifactPublished{
			Artifact: v.ID, Name: v.Name, Kind: v.Kind, Version: v.Version, By: v.By,
		}}}}
		events, err := newEvents(req)
		if err != nil {
			return err
		}
		if _, err := writeBatch(ctx, tx, req, events); err != nil {
			return err
		}
		out, err = artifactVersion(ctx, tx, v.ID, v.Version)
		return err
	})
	if err != nil {
		return proto.Artifact{}, err
	}
	return out, nil
}

// Artifacts returns a session's artifacts at their latest versions, the
// most recently published first.
func (s *Store) Artifacts(ctx context.Context, session proto.SessionID) ([]proto.Artifact, error) {
	rows, err := s.rdb.QueryContext(ctx, artifactSelect+` WHERE a.session_id = ?
		AND v.version = (SELECT max(version) FROM artifact_versions WHERE artifact_id = a.id)
		ORDER BY v.created_at DESC, a.id`, session)
	if err != nil {
		return nil, fmt.Errorf("store: list artifacts: %w", err)
	}
	defer rows.Close()
	var out []proto.Artifact
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list artifacts: %w", err)
	}
	return out, nil
}

// ArtifactByName returns a session's artifact named name at its latest
// version.
func (s *Store) ArtifactByName(ctx context.Context, session proto.SessionID, name string) (proto.Artifact, error) {
	return latestArtifact(ctx, s.rdb, `a.session_id = ? AND a.name = ?`, session, name)
}

// ArtifactVersion returns version n of an artifact; n = 0 is its latest.
func (s *Store) ArtifactVersion(ctx context.Context, id proto.ArtifactID, n int) (proto.Artifact, error) {
	if n == 0 {
		return latestArtifact(ctx, s.rdb, `a.id = ?`, id)
	}
	return artifactVersion(ctx, s.rdb, id, n)
}

// ArtifactVersions returns every recorded version number of an artifact
// in a session, by artifact ID.
func (s *Store) ArtifactVersions(ctx context.Context, session proto.SessionID) (map[proto.ArtifactID][]int, error) {
	rows, err := s.rdb.QueryContext(ctx, `SELECT a.id, v.version FROM artifacts a
		JOIN artifact_versions v ON v.artifact_id = a.id WHERE a.session_id = ? ORDER BY a.id, v.version`, session)
	if err != nil {
		return nil, fmt.Errorf("store: list artifact versions: %w", err)
	}
	defer rows.Close()
	out := map[proto.ArtifactID][]int{}
	for rows.Next() {
		var id proto.ArtifactID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("store: list artifact versions: %w", err)
		}
		out[id] = append(out[id], n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list artifact versions: %w", err)
	}
	return out, nil
}

// isConstraint reports whether err is a primary key or unique violation.
func isConstraint(err error) bool {
	var serr *sqlite.Error
	return errors.As(err, &serr) &&
		(serr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY || serr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE)
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const artifactSelect = `SELECT a.id, a.session_id, a.name, a.kind, v.version, v.entry, v.by, v.files, v.size,
	a.created_at, v.created_at FROM artifacts a JOIN artifact_versions v ON v.artifact_id = a.id`

func latestArtifact(ctx context.Context, q querier, where string, args ...any) (proto.Artifact, error) {
	return getArtifact(ctx, q, artifactSelect+` WHERE `+where+` ORDER BY v.version DESC LIMIT 1`, args...)
}

func artifactVersion(ctx context.Context, q querier, id proto.ArtifactID, n int) (proto.Artifact, error) {
	return getArtifact(ctx, q, artifactSelect+` WHERE a.id = ? AND v.version = ?`, id, n)
}

func getArtifact(ctx context.Context, q querier, query string, args ...any) (proto.Artifact, error) {
	a, err := scanArtifact(q.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return proto.Artifact{}, fmt.Errorf("%w: artifact", ErrNotFound)
	}
	return a, err
}

func scanArtifact(r scanner) (proto.Artifact, error) {
	var a proto.Artifact
	var created, updated int64
	err := r.Scan(&a.ID, &a.Session, &a.Name, &a.Kind, &a.Version, &a.Entry, &a.By, &a.Files, &a.Size, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return proto.Artifact{}, err
	}
	if err != nil {
		return proto.Artifact{}, fmt.Errorf("store: read artifact: %w", err)
	}
	a.CreatedAt = time.UnixMilli(created).UTC()
	a.UpdatedAt = time.UnixMilli(updated).UTC()
	return a, nil
}
