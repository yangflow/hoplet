import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * What the user did to a session in Hoplet, beside what its vendor wrote: a title of their own, when it
 * was pinned and when it was archived. All three are null until set, and ingest never touches them.
 */
export const sessionActions: Migration = {
  id: 11,
  name: 'session-actions',
  up: (db: DatabaseSync) => {
    db.exec('ALTER TABLE sessions ADD COLUMN title_override TEXT');
    db.exec('ALTER TABLE sessions ADD COLUMN pinned_at INTEGER');
    db.exec('ALTER TABLE sessions ADD COLUMN archived_at INTEGER');
  },
};
