import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Permission rules (#98, ADR 0029). `permission_rules` holds the rules the user made: `scope` is global, project
 * (`device_id` and `project_path`) or session (`session_id`); `match` is the JSON of the matcher; `position` keeps
 * the order they were made in. The built-in rules are not rows: `permission_builtins` keeps the switch of one the
 * user changed. `permission_revisions` counts changes per machine; the row with an empty `device_id` counts the
 * changes that reach every machine.
 */
export const permissionRules: Migration = {
  id: 25,
  name: 'permission-rules',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE permission_rules(
        id TEXT PRIMARY KEY,
        position INTEGER NOT NULL,
        scope TEXT NOT NULL,
        device_id TEXT,
        project_path TEXT,
        session_id TEXT,
        tool TEXT NOT NULL,
        match TEXT NOT NULL,
        action TEXT NOT NULL,
        note TEXT NOT NULL,
        enabled INTEGER NOT NULL,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE INDEX permission_rules_session ON permission_rules(session_id);
      CREATE INDEX permission_rules_project ON permission_rules(device_id, project_path);
      CREATE TABLE permission_builtins(
        id TEXT PRIMARY KEY,
        enabled INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE TABLE permission_revisions(
        device_id TEXT PRIMARY KEY,
        revision INTEGER NOT NULL
      );
    `);
  },
};
