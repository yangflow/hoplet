import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * How the core ended a child (ADR 0035): `timed-out` when a limit of its tree stopped it, `cancelled` when its
 * parent cancelled it. NULL while nothing of the kind happened. The parent's `wait_children` reports it.
 */
export const childEnded: Migration = {
  id: 36,
  name: 'child-ended',
  up: (db: DatabaseSync) => {
    db.exec('ALTER TABLE session_children ADD COLUMN ended TEXT;');
  },
};
