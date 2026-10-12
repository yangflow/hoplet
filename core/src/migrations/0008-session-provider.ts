import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/** Records the vendor of every session (ADR 0012). Every existing session is a Claude Code session. */
export const sessionProviderColumn: Migration = {
  id: 8,
  name: 'session-provider-column',
  up: (db: DatabaseSync) => {
    db.exec(`ALTER TABLE sessions ADD COLUMN provider TEXT NOT NULL DEFAULT 'claude'`);
  },
};
