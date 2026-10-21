import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Session templates (#76). A template holds the parts of a new session request that a person types again and
 * again; `config` is the JSON of the driver's options. `device_id` and `project_path` may be NULL: the starting
 * window then asks for them. Names are unique.
 */
export const templates: Migration = {
  id: 24,
  name: 'templates',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE templates(
        id TEXT PRIMARY KEY,
        name TEXT NOT NULL UNIQUE,
        text TEXT NOT NULL,
        device_id TEXT,
        project_path TEXT,
        provider TEXT NOT NULL,
        configuration TEXT,
        config TEXT,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
    `);
  },
};
