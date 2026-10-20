import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * What a device's agent last detected on its machine (`inventory`, the JSON of a `device.inventory`) and the
 * labels the host gave it (`labels`, a JSON array of strings; NULL is none, the same as `[]`). `inventory` is NULL
 * until the agent reports, and both survive the device going offline (#70).
 */
export const deviceInventory: Migration = {
  id: 20,
  name: 'device-inventory',
  up: (db: DatabaseSync) => {
    db.exec(`ALTER TABLE devices ADD COLUMN inventory TEXT`);
    db.exec(`ALTER TABLE devices ADD COLUMN labels TEXT`);
  },
};
