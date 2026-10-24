// Review comments on the lines of a session's diff (#84).
import type { Annotation, AnnotationSide, AnnotationState, ReviewDelivery } from '@hoplet/shared';
import type { StoreContext } from './layout.ts';

interface Row {
  annotation_id: string;
  path: string;
  side: string;
  start_line: number;
  end_line: number;
  snippet: string;
  body: string;
  state: string;
  created_at: number;
  updated_at: number;
  review: number | null;
  sent_at: number | null;
  delivery: string | null;
  message_id: string | null;
}

const COLUMNS =
  'annotation_id, path, side, start_line, end_line, snippet, body, state, created_at, updated_at, review, sent_at, delivery, message_id';

function annotationOf(row: Row): Annotation {
  return {
    id: row.annotation_id,
    path: row.path,
    side: row.side as AnnotationSide,
    startLine: row.start_line,
    endLine: row.end_line,
    snippet: row.snippet,
    body: row.body,
    state: row.state as AnnotationState,
    createdAt: row.created_at,
    updatedAt: row.updated_at,
    ...(row.review === null
      ? {}
      : {
          sent: {
            review: row.review,
            at: row.sent_at ?? 0,
            delivery: row.delivery as ReviewDelivery,
            messageId: row.message_id ?? '',
          },
        }),
  };
}

/** A session's comments, in the order they were added. */
export function listAnnotations(ctx: StoreContext, sessionId: string): Annotation[] {
  const rows = ctx.db
    .prepare(`SELECT ${COLUMNS} FROM session_annotations WHERE session_id = ? ORDER BY seq`)
    .all(sessionId) as unknown as Row[];
  return rows.map(annotationOf);
}

export function getAnnotation(ctx: StoreContext, sessionId: string, id: string): Annotation | null {
  const row = ctx.db
    .prepare(
      `SELECT ${COLUMNS} FROM session_annotations WHERE session_id = ? AND annotation_id = ?`,
    )
    .get(sessionId, id) as Row | undefined;
  return row ? annotationOf(row) : null;
}

export function countAnnotations(ctx: StoreContext, sessionId: string): number {
  const row = ctx.db
    .prepare('SELECT COUNT(*) AS n FROM session_annotations WHERE session_id = ?')
    .get(sessionId) as { n: number };
  return row.n;
}

export type NewAnnotation = Omit<Annotation, 'state' | 'createdAt' | 'updatedAt' | 'sent'>;

/** Stores a pending comment; a repeated ID keeps the stored one. Returns the comment as stored. */
export function insertAnnotation(
  ctx: StoreContext,
  sessionId: string,
  a: NewAnnotation,
  at: number,
): Annotation {
  ctx.db
    .prepare(
      `INSERT OR IGNORE INTO session_annotations(session_id, annotation_id, path, side, start_line, end_line, snippet, body, state, created_at, updated_at)
       VALUES(?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
    )
    .run(sessionId, a.id, a.path, a.side, a.startLine, a.endLine, a.snippet, a.body, at, at);
  const stored = getAnnotation(ctx, sessionId, a.id);
  if (!stored) throw new Error('annotation not stored');
  return stored;
}

/** Replaces the text of a pending comment; null when it is not pending. */
export function editAnnotation(
  ctx: StoreContext,
  sessionId: string,
  id: string,
  body: string,
  at: number,
): Annotation | null {
  const changed = ctx.db
    .prepare(
      `UPDATE session_annotations SET body = ?, updated_at = ? WHERE session_id = ? AND annotation_id = ? AND state = 'pending'`,
    )
    .run(body, at, sessionId, id).changes;
  return changed ? getAnnotation(ctx, sessionId, id) : null;
}

/** Moves a comment from `from` to `to` (resolving or reopening a sent one); null when it is not in `from`. */
export function moveAnnotation(
  ctx: StoreContext,
  sessionId: string,
  id: string,
  from: AnnotationState,
  to: AnnotationState,
  at: number,
): Annotation | null {
  const changed = ctx.db
    .prepare(
      `UPDATE session_annotations SET state = ?, updated_at = ? WHERE session_id = ? AND annotation_id = ? AND state = ?`,
    )
    .run(to, at, sessionId, id, from).changes;
  return changed ? getAnnotation(ctx, sessionId, id) : null;
}

export function deleteAnnotation(ctx: StoreContext, sessionId: string, id: string): boolean {
  return (
    ctx.db
      .prepare('DELETE FROM session_annotations WHERE session_id = ? AND annotation_id = ?')
      .run(sessionId, id).changes > 0
  );
}

/** The number the session's next review gets. */
export function nextReview(ctx: StoreContext, sessionId: string): number {
  const row = ctx.db
    .prepare('SELECT MAX(review) AS n FROM session_annotations WHERE session_id = ?')
    .get(sessionId) as { n: number | null };
  return (row.n ?? 0) + 1;
}

/**
 * Marks the pending comments in `ids` sent in one review, in one transaction; false, changing nothing, when one
 * of them is no longer pending.
 */
export function markSent(
  ctx: StoreContext,
  sessionId: string,
  ids: readonly string[],
  sent: { review: number; at: number; delivery: ReviewDelivery; messageId: string },
): boolean {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const update = ctx.db.prepare(
      `UPDATE session_annotations SET state = 'sent', updated_at = ?, review = ?, sent_at = ?, delivery = ?, message_id = ?
       WHERE session_id = ? AND annotation_id = ? AND state = 'pending'`,
    );
    for (const id of ids) {
      const changed = update.run(
        sent.at,
        sent.review,
        sent.at,
        sent.delivery,
        sent.messageId,
        sessionId,
        id,
      ).changes;
      if (!changed) {
        ctx.db.exec('ROLLBACK');
        return false;
      }
    }
    ctx.db.exec('COMMIT');
    return true;
  } catch (err) {
    ctx.db.exec('ROLLBACK');
    throw err;
  }
}
