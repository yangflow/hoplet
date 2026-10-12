import type { Migration } from './index.ts';

export const storageRecovery: Migration = {
  id: 6,
  name: 'storage-recovery',
  up: (db) => {
    db.exec(`
      CREATE TABLE append_recovery(
        session_id TEXT PRIMARY KEY,
        first_seq INTEGER NOT NULL CHECK(first_seq >= 0),
        tail_bytes INTEGER NOT NULL CHECK(tail_bytes >= 0),
        last_seq INTEGER NOT NULL CHECK(last_seq >= first_seq)
      );
      CREATE TABLE compression_pending(
        session_id TEXT NOT NULL,
        seq INTEGER NOT NULL CHECK(seq >= 0),
        PRIMARY KEY(session_id, seq)
      );
    `);
  },
};
