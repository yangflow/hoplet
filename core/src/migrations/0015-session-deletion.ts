import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Session deletion (ADR 0023). `session_worktrees` records the worktree a handoff created for a session: the
 * clone it belongs to, the worktree directory and the branch, on the device that received the session, and the
 * device that owned it before (`from_device_id`). `session_tombstones` holds the ID of a deleted session so that
 * ingest refuses it; `forgotten` is 1 once the owner's agent confirmed it stopped syncing the session.
 */
export const sessionDeletion: Migration = {
  id: 15,
  name: 'session-deletion',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE session_worktrees(
        session_id     TEXT PRIMARY KEY,
        device_id      TEXT NOT NULL,
        from_device_id TEXT NOT NULL,
        clone          TEXT NOT NULL,
        worktree       TEXT NOT NULL,
        branch         TEXT NOT NULL,
        created_at     INTEGER NOT NULL
      ) WITHOUT ROWID;
      CREATE TABLE session_tombstones(
        session_id TEXT PRIMARY KEY,
        device_id  TEXT NOT NULL,
        deleted_at INTEGER NOT NULL,
        forgotten  INTEGER NOT NULL DEFAULT 0
      ) WITHOUT ROWID;
      CREATE INDEX session_tombstones_by_device ON session_tombstones(device_id, forgotten);
    `);
  },
};
