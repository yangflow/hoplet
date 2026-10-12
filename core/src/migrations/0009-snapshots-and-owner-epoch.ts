import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * The latest workspace snapshot per session (the files are `snapshots/<sessionId>/<snapshotId>.tar.gz`) and
 * the owner epoch, which increases with every handoff.
 */
export const snapshotsAndOwnerEpoch: Migration = {
  id: 9,
  name: 'snapshots-and-owner-epoch',
  up: (db: DatabaseSync) => {
    db.exec(`
      ALTER TABLE sessions ADD COLUMN owner_epoch INTEGER NOT NULL DEFAULT 0;
      CREATE TABLE snapshots(
        session_id  TEXT PRIMARY KEY,
        snapshot_id TEXT NOT NULL,
        created_at  INTEGER NOT NULL,
        base_commit TEXT NOT NULL,
        origin_url  TEXT NOT NULL,
        tree_hash   TEXT NOT NULL,
        byte_size   INTEGER NOT NULL CHECK(byte_size > 0),
        sha256      TEXT NOT NULL
      );
    `);
  },
};
