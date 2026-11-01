package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yangflow/hoplet/proto"
)

// BaselineRepo is one repository's content when a session began: Tree is a
// tree id in Hoplet's own object directory for the repository. Error, when
// set, says why no snapshot could be taken; Tree is then empty.
type BaselineRepo struct {
	Path    string // relative to the session's working directory
	Tree    string
	Error   string
	Skipped []proto.SkippedFile
}

// Baseline is a session's starting point. Error, when set, says why it is
// incomplete (repositories could not be found, or time ran out); the
// repositories it does hold are still good.
type Baseline struct {
	TakenAt time.Time
	Error   string
	Repos   []BaselineRepo
}

// SaveBaseline records the session's baseline, replacing any earlier one.
func (s *Store) SaveBaseline(ctx context.Context, id proto.SessionID, b Baseline) error {
	return s.tx(ctx, false, func(tx *sql.Tx) error {
		if err := requireSession(ctx, tx, id); err != nil {
			return err
		}
		// A new baseline replaces the old one's repositories.
		if _, err := tx.ExecContext(ctx, `DELETE FROM baseline_repos WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("store: clear baseline: %w", err)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO baselines (session_id, taken_at, error) VALUES (?, ?, ?)
			ON CONFLICT (session_id) DO UPDATE SET taken_at = excluded.taken_at, error = excluded.error`,
			id, b.TakenAt.UnixMilli(), b.Error)
		if err != nil {
			return fmt.Errorf("store: save baseline: %w", err)
		}
		for _, r := range b.Repos {
			skipped, err := json.Marshal(append([]proto.SkippedFile{}, r.Skipped...))
			if err != nil {
				return fmt.Errorf("store: encode skipped files: %w", err)
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO baseline_repos (session_id, repo, tree, skipped, error)
				VALUES (?, ?, ?, ?, ?)`, id, r.Path, r.Tree, string(skipped), r.Error)
			if err != nil {
				return fmt.Errorf("store: save baseline repo %s: %w", r.Path, err)
			}
		}
		return nil
	})
}

// Baseline returns the session's baseline, with ok false when none was
// recorded. It returns ErrNotFound for an unknown session.
func (s *Store) Baseline(ctx context.Context, id proto.SessionID) (b Baseline, ok bool, err error) {
	err = s.readTx(ctx, func(tx *sql.Tx) error {
		if err := requireSession(ctx, tx, id); err != nil {
			return err
		}
		var taken int64
		err := tx.QueryRowContext(ctx, `SELECT taken_at, error FROM baselines WHERE session_id = ?`, id).Scan(&taken, &b.Error)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("store: read baseline: %w", err)
		}
		ok = true
		b.TakenAt = time.UnixMilli(taken).UTC()
		rows, err := tx.QueryContext(ctx, `SELECT repo, tree, skipped, error FROM baseline_repos
			WHERE session_id = ? ORDER BY repo`, id)
		if err != nil {
			return fmt.Errorf("store: read baseline repos: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var r BaselineRepo
			var skipped string
			if err := rows.Scan(&r.Path, &r.Tree, &skipped, &r.Error); err != nil {
				return fmt.Errorf("store: read baseline repo: %w", err)
			}
			if err := json.Unmarshal([]byte(skipped), &r.Skipped); err != nil {
				return fmt.Errorf("store: decode skipped files of %s: %w", r.Path, err)
			}
			if len(r.Skipped) == 0 {
				r.Skipped = nil
			}
			b.Repos = append(b.Repos, r)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("store: read baseline repos: %w", err)
		}
		return nil
	})
	if err != nil {
		return Baseline{}, false, err
	}
	return b, ok, nil
}
