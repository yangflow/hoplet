import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Drops session links and the provider column, and removes every session whose provider is
 * 'zcode'. The index rows go in this transaction; their chunk directories are queued in
 * `session_purge` in the same transaction and removed by the store at startup, before it serves
 * anything. A crash anywhere leaves either the old schema or the new one plus the queue.
 */
export const removeLinksAndZcode: Migration = {
  id: 7,
  name: 'remove-links-and-zcode',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE session_purge(session_id TEXT PRIMARY KEY);
      INSERT INTO session_purge(session_id) SELECT session_id FROM sessions WHERE provider = 'zcode';
      DELETE FROM append_recovery WHERE session_id IN (SELECT session_id FROM session_purge);
      DELETE FROM compression_pending WHERE session_id IN (SELECT session_id FROM session_purge);
      DELETE FROM sessions WHERE session_id IN (SELECT session_id FROM session_purge);
      DROP TABLE session_links;
      ALTER TABLE sessions DROP COLUMN provider;
    `);
  },
};
