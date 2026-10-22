import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * How far the audit log's tool-call deriver has read each session (#80, ADR 0030): `line` is the number of lines
 * of the session already derived, so the next one to read has that index. A session gets its row with its next
 * append, set to the lines it already had, so only lines that arrive after the feature are derived.
 */
export const auditCursor: Migration = {
  id: 27,
  name: 'audit-cursor',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE audit_cursor(
        session_id TEXT PRIMARY KEY,
        line INTEGER NOT NULL
      );
    `);
  },
};
