package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/yangflow/hoplet/proto"
)

// ClearVoiceTokens removes the tool tokens of all voice sessions. A call's
// token is good only for that call.
func (s *Store) ClearVoiceTokens(ctx context.Context) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM session_tokens
			WHERE session_id IN (SELECT id FROM sessions WHERE mode = ?)`, proto.ModeVoice); err != nil {
			return fmt.Errorf("store: clear voice tokens: %w", err)
		}
		return nil
	})
}

// LatestReply returns the text of the session's latest assistant message
// with any text in it; empty when there is none.
func (s *Store) LatestReply(ctx context.Context, id proto.SessionID) (string, error) {
	var text string
	err := s.rdb.QueryRowContext(ctx, `SELECT json_extract(payload, '$.text') FROM events
		WHERE session_id = ? AND type = ? AND agent IS NULL
			AND json_extract(payload, '$.role') = ? AND trim(json_extract(payload, '$.text')) != ''
		ORDER BY seq DESC LIMIT 1`, id, proto.TypeMessage, proto.RoleAssistant).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: latest reply of %s: %w", id, err)
	}
	return text, nil
}
