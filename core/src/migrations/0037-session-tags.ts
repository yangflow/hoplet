import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * The tags the user gave a session (docs/spec.md#organizing-sessions): lowercase, global across machines, at
 * most ten a session. Ingest never touches them; purging the session removes its tags.
 */
export const sessionTags: Migration = {
  id: 37,
  name: 'session-tags',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE session_tags (
        session_id TEXT NOT NULL,
        tag TEXT NOT NULL,
        added_at INTEGER NOT NULL,
        PRIMARY KEY (session_id, tag)
      );
      CREATE INDEX session_tags_tag ON session_tags(tag);
    `);
  },
};
