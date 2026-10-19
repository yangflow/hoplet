import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * The system a device's agent runs on, as it announced it in `device.hello`: Go's `GOOS` and `GOARCH`. Both are
 * NULL for a device whose agent never said, so an offline machine keeps its mark.
 */
export const deviceSystem: Migration = {
  id: 17,
  name: 'device-system',
  up: (db: DatabaseSync) => {
    db.exec(`ALTER TABLE devices ADD COLUMN os TEXT`);
    db.exec(`ALTER TABLE devices ADD COLUMN arch TEXT`);
  },
};
