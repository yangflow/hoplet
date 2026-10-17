import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Session continuations (ADR 0025). A row says the plan of `from_session_id` was approved into the new session
 * `to_session_id`; `request_id` is the plan request and `mode` the permission mode it was approved with. A session
 * keeps its latest continuation. There are no foreign keys: the new session is not in the index until its first
 * lines sync.
 */
export const sessionContinuations: Migration = {
  id: 16,
  name: 'session-continuations',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE session_continuations(
        from_session_id TEXT PRIMARY KEY,
        to_session_id   TEXT NOT NULL UNIQUE,
        request_id      TEXT NOT NULL,
        mode            TEXT NOT NULL,
        created_at      INTEGER NOT NULL
      ) WITHOUT ROWID;
    `);
  },
};
