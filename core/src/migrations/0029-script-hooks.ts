import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Script hooks (#97, ADR 0031). They are declared in an agent's own `config.json`; `script_hooks` is what each
 * device announced in its last `device.hello`, plus the one thing the core owns: `enabled`. A hook that is no
 * longer announced is deleted, and the switch of a hook that comes back is the default again. A delivery of a
 * script hook is a `hook_deliveries` row whose `hook_id` is `script:<deviceId>:<slug>`; `run` is the JSON of what
 * the agent reported (exit code, duration, the first kilobyte of standard error).
 */
export const scriptHooks: Migration = {
  id: 29,
  name: 'script-hooks',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE script_hooks(
        device_id TEXT NOT NULL,
        id TEXT NOT NULL,
        command TEXT NOT NULL,
        events TEXT NOT NULL,
        projects TEXT NOT NULL,
        timeout_ms INTEGER NOT NULL,
        env TEXT NOT NULL,
        runs INTEGER NOT NULL,
        failed INTEGER NOT NULL,
        dropped INTEGER NOT NULL,
        enabled INTEGER NOT NULL DEFAULT 1,
        updated_at INTEGER NOT NULL,
        PRIMARY KEY(device_id, id)
      );
      ALTER TABLE hook_deliveries ADD COLUMN run TEXT;
    `);
  },
};
