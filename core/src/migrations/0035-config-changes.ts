import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Sessions change their own configuration (#69, ADR 0036): the `read-config` and `write-config` abilities in the
 * session MCP settings (reading is on by default, writing off), and the requests a session makes. `objects` holds
 * the proposed objects with their diffs; `previous` holds the content each applied object had before, kept for
 * undo and set to NULL when the retention ends. A request is kept after it closes so the turn can show it.
 */
export const configChanges: Migration = {
  id: 35,
  name: 'config-changes',
  up: (db: DatabaseSync) => {
    db.exec(`
      ALTER TABLE session_mcp_settings ADD COLUMN read_config INTEGER NOT NULL DEFAULT 1;
      ALTER TABLE session_mcp_settings ADD COLUMN write_config INTEGER NOT NULL DEFAULT 0;
      ALTER TABLE session_mcp_defaults ADD COLUMN read_config INTEGER NOT NULL DEFAULT 1;
      ALTER TABLE session_mcp_defaults ADD COLUMN write_config INTEGER NOT NULL DEFAULT 0;
      CREATE TABLE config_changes(
        change_id TEXT PRIMARY KEY,
        session_id TEXT NOT NULL,
        state TEXT NOT NULL,
        reason TEXT NOT NULL,
        objects TEXT NOT NULL,
        previous TEXT,
        created_at INTEGER NOT NULL,
        expires_at INTEGER NOT NULL,
        decided_at INTEGER,
        applied_at INTEGER,
        undone_at INTEGER,
        error TEXT,
        error_reason TEXT
      );
      CREATE INDEX config_changes_session ON config_changes(session_id, created_at);
      CREATE INDEX config_changes_open ON config_changes(state) WHERE state IN ('pending', 'approved');
      CREATE INDEX config_changes_previous ON config_changes(applied_at) WHERE previous IS NOT NULL;
    `);
  },
};
