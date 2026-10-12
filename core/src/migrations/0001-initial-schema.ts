import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/** Session metadata (the index) and the devices that push. Chunk files on disk hold the lines. */
export const initialSchema: Migration = {
  id: 1,
  name: 'initial-schema',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE IF NOT EXISTS sessions(
        session_id      TEXT PRIMARY KEY,
        project_path    TEXT NOT NULL DEFAULT '',
        project_dir     TEXT NOT NULL DEFAULT '',
        title           TEXT NOT NULL DEFAULT '',
        created_at      INTEGER NOT NULL,
        updated_at      INTEGER NOT NULL,
        line_count      INTEGER NOT NULL DEFAULT 0,
        byte_size       INTEGER NOT NULL DEFAULT 0,
        chunk_count     INTEGER NOT NULL DEFAULT 1,
        cur_chunk_lines INTEGER NOT NULL DEFAULT 0,
        device_id       TEXT NOT NULL DEFAULT ''
      );
      CREATE TABLE IF NOT EXISTS devices(
        device_id  TEXT PRIMARY KEY,
        name       TEXT NOT NULL DEFAULT '',
        version    TEXT NOT NULL DEFAULT '',
        first_seen INTEGER NOT NULL,
        last_seen  INTEGER NOT NULL
      );
    `);
  },
};
