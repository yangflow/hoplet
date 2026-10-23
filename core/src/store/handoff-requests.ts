// Hand-off requests of sessions (#67, ADR 0034).
import type { HandoffDecision, HandoffRequestState, SessionHandoffRequest } from '@hoplet/shared';
import { type AuditInput, auditedChange } from './audit.ts';
import type { StoreContext } from './layout.ts';

interface Row {
  request_id: string;
  session_id: string;
  target_device_id: string;
  reason: string;
  state: string;
  decision: string | null;
  created_at: number;
  expires_at: number;
  decided_at: number | null;
  error: string | null;
  error_reason: string | null;
}

const COLUMNS =
  'request_id, session_id, target_device_id, reason, state, decision, created_at, expires_at, decided_at, error, error_reason';

function requestOf(row: Row): SessionHandoffRequest {
  return {
    requestId: row.request_id,
    sessionId: row.session_id,
    targetDeviceId: row.target_device_id,
    reason: row.reason,
    state: row.state as HandoffRequestState,
    decision: row.decision as HandoffDecision | null,
    createdAt: row.created_at,
    expiresAt: row.expires_at,
    decidedAt: row.decided_at,
    ...(row.error === null ? {} : { error: row.error }),
    ...(row.error_reason === null ? {} : { errorReason: row.error_reason }),
  };
}

/** Stores a new request together with its audit record. */
export function insertRequest(
  ctx: StoreContext,
  request: SessionHandoffRequest,
  audit: AuditInput,
): SessionHandoffRequest {
  return auditedChange(
    ctx,
    () => {
      ctx.db
        .prepare(`INSERT INTO handoff_requests(${COLUMNS}) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
        .run(
          request.requestId,
          request.sessionId,
          request.targetDeviceId,
          request.reason,
          request.state,
          request.decision,
          request.createdAt,
          request.expiresAt,
          request.decidedAt,
          request.error ?? null,
          request.errorReason ?? null,
        );
      return request;
    },
    () => audit,
  );
}

export function getRequest(ctx: StoreContext, requestId: string): SessionHandoffRequest | null {
  const row = ctx.db
    .prepare(`SELECT ${COLUMNS} FROM handoff_requests WHERE request_id = ?`)
    .get(requestId) as Row | undefined;
  return row ? requestOf(row) : null;
}

/** A session's requests, newest first. */
export function listRequests(ctx: StoreContext, sessionId: string): SessionHandoffRequest[] {
  const rows = ctx.db
    .prepare(
      `SELECT ${COLUMNS} FROM handoff_requests WHERE session_id = ? ORDER BY created_at DESC, request_id DESC`,
    )
    .all(sessionId) as unknown as Row[];
  return rows.map(requestOf);
}

/** The requests that are still open: `pending` or `approved`. */
export function listOpenRequests(ctx: StoreContext): SessionHandoffRequest[] {
  const rows = ctx.db
    .prepare(
      `SELECT ${COLUMNS} FROM handoff_requests WHERE state IN ('pending', 'approved') ORDER BY created_at`,
    )
    .all() as unknown as Row[];
  return rows.map(requestOf);
}

export interface RequestChange {
  state: HandoffRequestState;
  decision?: HandoffDecision | null;
  decidedAt?: number | null;
  error?: string | null;
  errorReason?: string | null;
}

/**
 * Moves a request from one of the states in `from` to `change.state`; null when it is no longer in one of them (it
 * was decided or closed meanwhile). The audit record, when given, is written in the same transaction.
 */
export function changeRequest(
  ctx: StoreContext,
  requestId: string,
  from: readonly HandoffRequestState[],
  change: RequestChange,
  audit: AuditInput | null,
): SessionHandoffRequest | null {
  return auditedChange(
    ctx,
    () => {
      const current = getRequest(ctx, requestId);
      if (!current || !from.includes(current.state)) return null;
      ctx.db
        .prepare(
          `UPDATE handoff_requests SET state = ?, decision = ?, decided_at = ?, error = ?, error_reason = ? WHERE request_id = ?`,
        )
        .run(
          change.state,
          change.decision === undefined ? current.decision : change.decision,
          change.decidedAt === undefined ? current.decidedAt : change.decidedAt,
          change.error === undefined ? (current.error ?? null) : change.error,
          change.errorReason === undefined ? (current.errorReason ?? null) : change.errorReason,
          requestId,
        );
      return getRequest(ctx, requestId);
    },
    (result) => (result ? audit : null),
  );
}

/** How many requests of the session were approved automatically since `since` (Unix ms). */
export function countAutoApproved(ctx: StoreContext, sessionId: string, since: number): number {
  const row = ctx.db
    .prepare(
      `SELECT COUNT(*) AS n FROM handoff_requests WHERE session_id = ? AND decision = 'auto' AND decided_at >= ?`,
    )
    .get(sessionId, since) as { n: number };
  return row.n;
}

export function forgetRequests(ctx: StoreContext, sessionId: string): void {
  ctx.db.prepare(`DELETE FROM handoff_requests WHERE session_id = ?`).run(sessionId);
}
