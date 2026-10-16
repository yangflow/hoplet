/** Forward-only data migrations, applied in order at startup (docs/architecture.md#a-storage-migration). */
import type { DatabaseSync } from 'node:sqlite';
import { initialSchema } from './0001-initial-schema.ts';
import { sessionProvider } from './0002-session-provider.ts';
import { deviceTokenHash } from './0003-device-token-hash.ts';
import { sessionLinks } from './0004-session-links.ts';
import { sessionLinksDirected } from './0005-session-links-directed.ts';
import { storageRecovery } from './0006-storage-recovery.ts';
import { removeLinksAndZcode } from './0007-remove-links-and-zcode.ts';
import { sessionProviderColumn } from './0008-session-provider.ts';
import { snapshotsAndOwnerEpoch } from './0009-snapshots-and-owner-epoch.ts';
import { sessionGitBranch } from './0010-session-git-branch.ts';
import { sessionActions } from './0011-session-actions.ts';
import { directoryCommands } from './0012-directory-commands.ts';
import { sessionQueue } from './0013-session-queue.ts';
import { notifications } from './0014-notifications.ts';
import { sessionDeletion } from './0015-session-deletion.ts';

export interface Migration {
  readonly id: number;
  readonly name: string;
  readonly up: (db: DatabaseSync) => void;
}

const MIGRATIONS: readonly Migration[] = [
  initialSchema,
  sessionProvider,
  deviceTokenHash,
  sessionLinks,
  sessionLinksDirected,
  storageRecovery,
  removeLinksAndZcode,
  sessionProviderColumn,
  snapshotsAndOwnerEpoch,
  sessionGitBranch,
  sessionActions,
  directoryCommands,
  sessionQueue,
  notifications,
  sessionDeletion,
];

export function migrate(db: DatabaseSync): void {
  db.exec(
    'CREATE TABLE IF NOT EXISTS schema_migrations(id INTEGER PRIMARY KEY, name TEXT NOT NULL)',
  );
  const applied = new Set(
    (db.prepare('SELECT id FROM schema_migrations').all() as { id: number }[]).map((r) =>
      Number(r.id),
    ),
  );
  const newest = Math.max(...MIGRATIONS.map((m) => m.id));
  if ([...applied].some((id) => id > newest)) {
    throw new Error('data schema is newer than this core');
  }
  for (const migration of MIGRATIONS) {
    if (applied.has(migration.id)) continue;
    if (migration.id === 1) db.exec('PRAGMA journal_mode=WAL');
    db.exec('BEGIN IMMEDIATE');
    try {
      migration.up(db);
      db.prepare('INSERT INTO schema_migrations(id, name) VALUES(?, ?)').run(
        migration.id,
        migration.name,
      );
      db.exec('COMMIT');
    } catch (err) {
      db.exec('ROLLBACK');
      throw err;
    }
  }
}
