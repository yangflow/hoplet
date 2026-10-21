import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Token usage per message (#75). `usage` holds one row per assistant message of a session, written in the same
 * transaction as the append that carried the lines; `usage_prices` the per-model prices the host overrode.
 * `sessions.usage_backfilled` is 0 for the sessions that existed when this migration ran, until the background
 * backfill has read their chunks; a session created later is complete from its first append. `usage_model` is
 * the model the latest `turn_context` line of a Codex rollout named, which the following token counts belong to.
 */
export const usage: Migration = {
  id: 21,
  name: 'usage',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE usage(
        session_id TEXT NOT NULL,
        message_id TEXT NOT NULL,
        model TEXT NOT NULL,
        at INTEGER NOT NULL,
        input_tokens INTEGER NOT NULL,
        output_tokens INTEGER NOT NULL,
        cache_write_tokens INTEGER NOT NULL,
        cache_read_tokens INTEGER NOT NULL,
        PRIMARY KEY(session_id, message_id)
      ) WITHOUT ROWID;
      CREATE INDEX usage_at ON usage(at);
      CREATE TABLE usage_prices(
        model TEXT PRIMARY KEY,
        input REAL NOT NULL,
        output REAL NOT NULL,
        cache_write REAL NOT NULL,
        cache_read REAL NOT NULL
      );
      ALTER TABLE sessions ADD COLUMN usage_backfilled INTEGER NOT NULL DEFAULT 1;
      ALTER TABLE sessions ADD COLUMN usage_model TEXT NOT NULL DEFAULT '';
      UPDATE sessions SET usage_backfilled = 0;
    `);
  },
};
