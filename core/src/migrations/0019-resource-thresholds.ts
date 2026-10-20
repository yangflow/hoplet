import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * The thresholds at which a machine warns about memory and disk (#175). `resource_thresholds` holds the one row
 * of settings as JSON: the default for every machine and the overrides by device ID.
 */
export const resourceThresholds: Migration = {
  id: 19,
  name: 'resource-thresholds',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE resource_thresholds(
        id   INTEGER PRIMARY KEY CHECK (id = 1),
        json TEXT NOT NULL
      );
    `);
  },
};
