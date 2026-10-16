import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * The messages a user queued for a session while it ran (ADR 0020), and whether the queue is paused. `position`
 * orders a session's items; `version` rises with every edit so a change made from a stale view is refused;
 * `state` is `sending` from the moment the core hands the item to the machine until the machine accepted it.
 */
export const sessionQueue: Migration = {
  id: 13,
  name: 'session-queue',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE session_queue(
        id          TEXT PRIMARY KEY,
        session_id  TEXT NOT NULL,
        position    INTEGER NOT NULL,
        text        TEXT NOT NULL,
        attachments TEXT NOT NULL,
        version     INTEGER NOT NULL,
        state       TEXT NOT NULL,
        created_at  INTEGER NOT NULL
      ) WITHOUT ROWID;
      CREATE INDEX session_queue_by_session ON session_queue(session_id, position);
      CREATE TABLE session_queue_state(
        session_id TEXT PRIMARY KEY,
        paused     TEXT NOT NULL,
        error      TEXT
      ) WITHOUT ROWID;
    `);
  },
};
