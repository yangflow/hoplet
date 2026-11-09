package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yangflow/hoplet/proto"
)

// MemorySettings returns the memory settings.
func (s *Store) MemorySettings(ctx context.Context) (proto.ProfileSettings, error) {
	var out proto.ProfileSettings
	err := s.rdb.QueryRowContext(ctx, `SELECT use_personal, use_code, extraction, extraction_harness, include_code, auto_save
		FROM memory_settings WHERE id = 1`).
		Scan(&out.UsePersonal, &out.UseCode, &out.Extraction, &out.ExtractionHarness, &out.IncludeCode, &out.AutoSave)
	if err != nil {
		return proto.ProfileSettings{}, fmt.Errorf("store: read memory settings: %w", err)
	}
	return out, nil
}

// SetMemorySettings replaces the memory settings.
func (s *Store) SetMemorySettings(ctx context.Context, in proto.ProfileSettings) error {
	return s.tx(ctx, true, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE memory_settings SET use_personal = ?, use_code = ?, extraction = ?,
			extraction_harness = ?, include_code = ?, auto_save = ? WHERE id = 1`,
			in.UsePersonal, in.UseCode, in.Extraction, in.ExtractionHarness, in.IncludeCode, in.AutoSave)
		if err != nil {
			return fmt.Errorf("store: set memory settings: %w", err)
		}
		return nil
	})
}
