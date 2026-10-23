import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Hand-off requests of sessions (#67, ADR 0034): what a session asked for, who decided (`user` or `auto`) and how
 * it ended. A request is kept after it closes so the turn can show it.
 */
export const handoffRequests: Migration = {
  id: 33,
  name: 'handoff-requests',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE handoff_requests(
        request_id TEXT PRIMARY KEY,
        session_id TEXT NOT NULL,
        target_device_id TEXT NOT NULL,
        reason TEXT NOT NULL,
        state TEXT NOT NULL,
        decision TEXT,
        created_at INTEGER NOT NULL,
        expires_at INTEGER NOT NULL,
        decided_at INTEGER,
        error TEXT,
        error_reason TEXT
      );
      CREATE INDEX handoff_requests_session ON handoff_requests(session_id, created_at);
      CREATE INDEX handoff_requests_open ON handoff_requests(state) WHERE state IN ('pending', 'approved');
    `);
  },
};
