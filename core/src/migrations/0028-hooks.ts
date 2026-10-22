import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Lifecycle hooks (#97, ADR 0031). `hooks` holds the webhooks the user made: `events` is the JSON list of event
 * types, `scope_device_id` and `scope_project_path` are both null for all projects. `hook_deliveries` is one row
 * per event a hook is sent, with the body kept so a retry sends the same bytes; a settled row is kept 14 days.
 * The signing secrets are not here: they are in the secrets file.
 */
export const hooks: Migration = {
  id: 28,
  name: 'hooks',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE hooks(
        id TEXT PRIMARY KEY,
        url TEXT NOT NULL,
        label TEXT NOT NULL,
        enabled INTEGER NOT NULL,
        events TEXT NOT NULL,
        scope_device_id TEXT,
        scope_project_path TEXT,
        summary INTEGER NOT NULL,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
      );
      CREATE TABLE hook_deliveries(
        id TEXT PRIMARY KEY,
        hook_id TEXT NOT NULL,
        event_id TEXT NOT NULL,
        type TEXT NOT NULL,
        state TEXT NOT NULL,
        attempts INTEGER NOT NULL,
        last_error TEXT,
        body TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        settled_at INTEGER,
        next_attempt_at INTEGER,
        expires_at INTEGER NOT NULL
      );
      CREATE INDEX hook_deliveries_hook ON hook_deliveries(hook_id, created_at);
      CREATE INDEX hook_deliveries_due ON hook_deliveries(state, next_attempt_at);
    `);
  },
};
