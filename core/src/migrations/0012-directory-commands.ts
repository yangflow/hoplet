import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * The commands and skills the latest running session of a machine and working directory offered, so a draft
 * for that directory can offer them before its session starts. One row per device and directory; `commands`
 * is the JSON list an agent reported (shared/src/commands.ts).
 */
export const directoryCommands: Migration = {
  id: 12,
  name: 'directory-commands',
  up: (db: DatabaseSync) => {
    db.exec(`CREATE TABLE directory_commands(
      device_id  TEXT NOT NULL,
      cwd        TEXT NOT NULL,
      commands   TEXT NOT NULL,
      updated_at INTEGER NOT NULL,
      PRIMARY KEY(device_id, cwd)
    ) WITHOUT ROWID`);
  },
};
