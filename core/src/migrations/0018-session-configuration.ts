import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * The provider configuration a session belongs to (ADR 0028), as its owner agent announces it with the session's
 * appends: the configuration's ID, empty for the vendor's own.
 */
export const sessionConfiguration: Migration = {
  id: 18,
  name: 'session-configuration',
  up: (db: DatabaseSync) => {
    db.exec(`ALTER TABLE sessions ADD COLUMN configuration TEXT NOT NULL DEFAULT ''`);
  },
};
