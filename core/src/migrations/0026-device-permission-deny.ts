import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * The deny rules a device's agent announced from its own configuration in `device.hello` (`permission_deny`,
 * ADR 0029): the JSON array of the rules, NULL when the agent announced none.
 */
export const devicePermissionDeny: Migration = {
  id: 26,
  name: 'device-permission-deny',
  up: (db: DatabaseSync) => {
    db.exec(`ALTER TABLE devices ADD COLUMN permission_deny TEXT`);
  },
};
