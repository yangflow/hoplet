import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/** Undirected links between existing sessions, kept in the index and never in JSONL. */
export const sessionLinks: Migration = {
  id: 4,
  name: 'session-links',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE IF NOT EXISTS session_links(
        session_a TEXT NOT NULL,
        session_b TEXT NOT NULL,
        kind TEXT NOT NULL DEFAULT 'related',
        created_at INTEGER NOT NULL,
        PRIMARY KEY (session_a, session_b),
        CHECK (session_a < session_b)
      )
    `);
  },
};
