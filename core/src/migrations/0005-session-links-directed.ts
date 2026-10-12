import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Drop CHECK (session_a < session_b) so directed rows can store parent→child without
 * lexicographic sort. Existing `related` rows already satisfy a < b.
 */
export const sessionLinksDirected: Migration = {
  id: 5,
  name: 'session-links-directed',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE session_links_new(
        session_a TEXT NOT NULL,
        session_b TEXT NOT NULL,
        kind TEXT NOT NULL DEFAULT 'related',
        created_at INTEGER NOT NULL,
        PRIMARY KEY (session_a, session_b)
      );
      INSERT INTO session_links_new(session_a, session_b, kind, created_at)
        SELECT session_a, session_b, kind, created_at FROM session_links;
      DROP TABLE session_links;
      ALTER TABLE session_links_new RENAME TO session_links;
    `);
  },
};
