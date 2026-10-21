import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * The audit log (#80, ADR 0030). One global, append-only table; `hash` is the SHA-256 of the previous record's
 * `hash` and the canonical JSON of the other columns, so an edit, a removal from the middle or an insertion
 * breaks the chain. `seq` is AUTOINCREMENT: a number is never reused, so a record removed from the tail shows as
 * a gap. `summary` holds canonical JSON text of at most 2 KiB.
 */
export const auditLog: Migration = {
  id: 23,
  name: 'audit-log',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE audit_events(
        seq INTEGER PRIMARY KEY AUTOINCREMENT,
        at INTEGER NOT NULL,
        recorded_at INTEGER NOT NULL,
        type TEXT NOT NULL,
        session_id TEXT,
        device_id TEXT,
        actor_kind TEXT NOT NULL,
        actor_id TEXT NOT NULL,
        channel TEXT NOT NULL,
        ref TEXT,
        summary TEXT NOT NULL,
        prev_hash TEXT NOT NULL,
        hash TEXT NOT NULL
      );
      CREATE INDEX audit_events_session ON audit_events(session_id, ref);
      CREATE INDEX audit_events_type ON audit_events(type, seq);
      CREATE INDEX audit_events_device ON audit_events(device_id, seq);
      CREATE INDEX audit_events_at ON audit_events(at);
    `);
  },
};
