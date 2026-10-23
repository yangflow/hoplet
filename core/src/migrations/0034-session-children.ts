import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * The sessions a session started (ADR 0035): the creation chain (root first, parent last), when the core started
 * the session and how long it may run. The relation is recorded once, at the start, and lives as long as the
 * session; the tree watcher and the limits read it. A deleted session loses its row.
 */
export const sessionChildren: Migration = {
  id: 34,
  name: 'session-children',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE session_children(
        session_id TEXT PRIMARY KEY,
        chain TEXT NOT NULL,
        started_at INTEGER NOT NULL,
        time_limit_ms INTEGER NOT NULL
      );
    `);
  },
};
