import type { DatabaseSync } from 'node:sqlite';
import type { Migration } from './index.ts';

/**
 * Review comments on the lines of a session's diff (#84): the anchor (path, side, line range and the lines'
 * text), the comment, its state, and for a sent one the review it went out in. `seq` keeps the order they were
 * added in.
 */
export const sessionAnnotations: Migration = {
  id: 38,
  name: 'session-annotations',
  up: (db: DatabaseSync) => {
    db.exec(`
      CREATE TABLE session_annotations(
        seq INTEGER PRIMARY KEY AUTOINCREMENT,
        annotation_id TEXT NOT NULL,
        session_id TEXT NOT NULL,
        path TEXT NOT NULL,
        side TEXT NOT NULL,
        start_line INTEGER NOT NULL,
        end_line INTEGER NOT NULL,
        snippet TEXT NOT NULL,
        body TEXT NOT NULL,
        state TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL,
        review INTEGER,
        sent_at INTEGER,
        delivery TEXT,
        message_id TEXT,
        UNIQUE(session_id, annotation_id)
      );
    `);
  },
};
