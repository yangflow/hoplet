import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/** Which agent provider synthesized the session stream. Missing values read as claude. */
export const sessionProvider: Migration = {
  id: 2,
  name: 'session-provider',
  up: (db: DatabaseSync) => {
    db.exec(`ALTER TABLE sessions ADD COLUMN provider TEXT NOT NULL DEFAULT 'claude'`);
  },
};
