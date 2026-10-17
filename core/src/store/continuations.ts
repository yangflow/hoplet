import type { StoreContext } from './layout.ts';

/** The session a session was continued in and the one it was continued from, as session IDs. */
export interface ContinuationLinks {
  continuedIn?: string;
  continuedFrom?: string;
}

/**
 * Records the continuation in one transaction. `from_session_id` is the key and `to_session_id` is unique, so a
 * row that already names either session is replaced: a session keeps its latest continuation.
 */
export function recordContinuation(
  ctx: StoreContext,
  fromSessionId: string,
  toSessionId: string,
  requestId: string,
  mode: string,
  now: number,
): void {
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    ctx.db
      .prepare('DELETE FROM session_continuations WHERE from_session_id = ? OR to_session_id = ?')
      .run(fromSessionId, toSessionId);
    ctx.db
      .prepare(
        `INSERT INTO session_continuations(from_session_id, to_session_id, request_id, mode, created_at)
         VALUES(?, ?, ?, ?, ?)`,
      )
      .run(fromSessionId, toSessionId, requestId, mode, now);
    ctx.db.exec('COMMIT');
  } catch (err) {
    ctx.db.exec('ROLLBACK');
    throw err;
  }
}

export function continuationLinks(ctx: StoreContext, sessionId: string): ContinuationLinks {
  const to = ctx.db
    .prepare('SELECT to_session_id FROM session_continuations WHERE from_session_id = ?')
    .get(sessionId) as { to_session_id: string } | undefined;
  const from = ctx.db
    .prepare('SELECT from_session_id FROM session_continuations WHERE to_session_id = ?')
    .get(sessionId) as { from_session_id: string } | undefined;
  return {
    ...(to ? { continuedIn: to.to_session_id } : {}),
    ...(from ? { continuedFrom: from.from_session_id } : {}),
  };
}
