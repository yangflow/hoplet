import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/** Hashed device bearer, bound to device_id (ADR 0005). */
export const deviceTokenHash: Migration = {
  id: 3,
  name: 'device-token-hash',
  up: (db: DatabaseSync) => {
    db.exec(`ALTER TABLE devices ADD COLUMN token_hash TEXT`);
    db.exec(`CREATE UNIQUE INDEX IF NOT EXISTS devices_token_hash ON devices(token_hash)`);
  },
};
