// Requests of sessions to change their own configuration (#69, ADR 0036), with the previous content of what they
// changed, which undo needs.
import type {
  ConfigChangeObject,
  ConfigChangeState,
  ConfigObjectKind,
  SessionConfigChange,
} from '@hoplet/shared';
import { type AuditInput, auditedChange } from './audit.ts';
import type { StoreContext } from './layout.ts';

/** What an applied object held before: null when it did not exist. */
export interface PreviousContent {
  kind: ConfigObjectKind;
  name: string;
  content: string | null;
}

/** A stored request: what the API shows, and the previous content the API never shows. */
export interface StoredConfigChange extends SessionConfigChange {
  previous: PreviousContent[] | null;
}

interface Row {
  change_id: string;
  session_id: string;
  state: string;
  reason: string;
  objects: string;
  previous: string | null;
  created_at: number;
  expires_at: number;
  decided_at: number | null;
  applied_at: number | null;
  undone_at: number | null;
  error: string | null;
  error_reason: string | null;
}

const COLUMNS =
  'change_id, session_id, state, reason, objects, previous, created_at, expires_at, decided_at, applied_at, undone_at, error, error_reason';

function parsed<T>(json: string | null, fallback: T): T {
  if (json === null) return fallback;
  try {
    return JSON.parse(json) as T;
  } catch {
    return fallback;
  }
}

function changeOf(row: Row): StoredConfigChange {
  const previous =
    row.previous === null ? null : parsed<PreviousContent[] | null>(row.previous, null);
  const state = row.state as ConfigChangeState;
  return {
    changeId: row.change_id,
    sessionId: row.session_id,
    state,
    reason: row.reason,
    objects: parsed<ConfigChangeObject[]>(row.objects, []),
    createdAt: row.created_at,
    expiresAt: row.expires_at,
    decidedAt: row.decided_at,
    appliedAt: row.applied_at,
    undoneAt: row.undone_at,
    undoable: state === 'applied' && previous !== null,
    ...(row.error === null ? {} : { error: row.error }),
    ...(row.error_reason === null ? {} : { errorReason: row.error_reason }),
    previous,
  };
}

/** The request as the API shows it: without the previous content. */
export function publicChange(change: StoredConfigChange): SessionConfigChange {
  const { previous: _previous, ...shown } = change;
  return shown;
}

/** Stores a new request together with its audit record. */
export function insertChange(
  ctx: StoreContext,
  change: StoredConfigChange,
  audit: AuditInput,
): StoredConfigChange {
  return auditedChange(
    ctx,
    () => {
      ctx.db
        .prepare(
          `INSERT INTO config_changes(${COLUMNS}) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
        )
        .run(
          change.changeId,
          change.sessionId,
          change.state,
          change.reason,
          JSON.stringify(change.objects),
          change.previous === null ? null : JSON.stringify(change.previous),
          change.createdAt,
          change.expiresAt,
          change.decidedAt,
          change.appliedAt,
          change.undoneAt,
          change.error ?? null,
          change.errorReason ?? null,
        );
      return change;
    },
    () => audit,
  );
}

export function getChange(ctx: StoreContext, changeId: string): StoredConfigChange | null {
  const row = ctx.db
    .prepare(`SELECT ${COLUMNS} FROM config_changes WHERE change_id = ?`)
    .get(changeId) as Row | undefined;
  return row ? changeOf(row) : null;
}

/** A session's requests, newest first. */
export function listChanges(ctx: StoreContext, sessionId: string): StoredConfigChange[] {
  const rows = ctx.db
    .prepare(
      `SELECT ${COLUMNS} FROM config_changes WHERE session_id = ? ORDER BY created_at DESC, change_id DESC`,
    )
    .all(sessionId) as unknown as Row[];
  return rows.map(changeOf);
}

/** The requests that are still open: `pending` or `approved`. */
export function listOpenChanges(ctx: StoreContext): StoredConfigChange[] {
  const rows = ctx.db
    .prepare(
      `SELECT ${COLUMNS} FROM config_changes WHERE state IN ('pending', 'approved') ORDER BY created_at`,
    )
    .all() as unknown as Row[];
  return rows.map(changeOf);
}

export interface ConfigChangePatch {
  state: ConfigChangeState;
  expiresAt?: number;
  decidedAt?: number | null;
  appliedAt?: number | null;
  undoneAt?: number | null;
  error?: string | null;
  errorReason?: string | null;
  previous?: PreviousContent[] | null;
}

/**
 * Moves a request from one of the states in `from` to `patch.state`; null when it is no longer in one of them (it
 * was decided or closed meanwhile). The audit record, when given, is written in the same transaction.
 */
export function patchChange(
  ctx: StoreContext,
  changeId: string,
  from: readonly ConfigChangeState[],
  patch: ConfigChangePatch,
  audit: AuditInput | null,
): StoredConfigChange | null {
  return auditedChange(
    ctx,
    () => {
      const current = getChange(ctx, changeId);
      if (!current || !from.includes(current.state)) return null;
      const previous = patch.previous === undefined ? current.previous : patch.previous;
      ctx.db
        .prepare(
          `UPDATE config_changes SET state = ?, expires_at = ?, decided_at = ?, applied_at = ?, undone_at = ?, error = ?,
             error_reason = ?, previous = ? WHERE change_id = ?`,
        )
        .run(
          patch.state,
          patch.expiresAt === undefined ? current.expiresAt : patch.expiresAt,
          patch.decidedAt === undefined ? current.decidedAt : patch.decidedAt,
          patch.appliedAt === undefined ? current.appliedAt : patch.appliedAt,
          patch.undoneAt === undefined ? current.undoneAt : patch.undoneAt,
          patch.error === undefined ? (current.error ?? null) : patch.error,
          patch.errorReason === undefined ? (current.errorReason ?? null) : patch.errorReason,
          previous === null ? null : JSON.stringify(previous),
          changeId,
        );
      return getChange(ctx, changeId);
    },
    (result) => (result ? audit : null),
  );
}

/**
 * Drops the previous content of changes applied before `before` (Unix ms); the records stay. Returns how many
 * lost it.
 */
export function pruneChangePrevious(ctx: StoreContext, before: number): number {
  return Number(
    ctx.db
      .prepare(
        `UPDATE config_changes SET previous = NULL WHERE previous IS NOT NULL AND applied_at < ?`,
      )
      .run(before).changes,
  );
}

export function forgetChanges(ctx: StoreContext, sessionId: string): void {
  ctx.db.prepare(`DELETE FROM config_changes WHERE session_id = ?`).run(sessionId);
}
