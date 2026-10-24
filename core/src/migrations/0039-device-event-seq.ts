import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * The highest journalled event of each machine the core has applied (ADR 0039). The welcome carries it, so an
 * agent replays only what the core has not applied; an event at or below it is acknowledged and skipped.
 */
export const deviceEventSeq: Migration = {
  id: 39,
  name: 'device-event-seq',
  up: (db: DatabaseSync) => {
    db.exec('ALTER TABLE devices ADD COLUMN event_seq INTEGER NOT NULL DEFAULT 0;');
  },
};
