import type { SessionMeta } from '@hoplet/shared';
import { isSessionId, ProviderSchema, parse } from '@hoplet/shared';
import { CHUNK_LINES, type StoreContext } from './layout.ts';

export interface SessionRow {
  session_id: string;
  project_path: string;
  project_dir: string;
  title: string;
  created_at: number;
  updated_at: number;
  line_count: number;
  byte_size: number;
  chunk_count: number;
  cur_chunk_lines: number;
  device_id: string;
  owner_epoch: number;
  provider: string;
  git_branch: string;
  title_override: string | null;
  pinned_at: number | null;
  archived_at: number | null;
  device_name?: string;
}

export function getRow(ctx: StoreContext, sessionId: string): SessionRow | undefined {
  if (!isSessionId(sessionId)) return undefined;
  return ctx.db.prepare('SELECT * FROM sessions WHERE session_id = ?').get(sessionId) as
    | SessionRow
    | undefined;
}

export function toMeta(row: SessionRow): SessionMeta {
  return {
    sessionId: row.session_id,
    projectPath: row.project_path,
    projectDir: row.project_dir,
    deviceName: row.device_name ?? '',
    title: row.title,
    provider: parse(ProviderSchema, row.provider),
    createdAt: Number(row.created_at),
    updatedAt: Number(row.updated_at),
    lineCount: Number(row.line_count),
    byteSize: Number(row.byte_size),
    ownerEpoch: Number(row.owner_epoch),
    ...(row.device_id ? { ownerDeviceId: row.device_id } : {}),
    ...(row.git_branch ? { gitBranch: row.git_branch } : {}),
  };
}

export function sessionCount(ctx: StoreContext): number {
  const r = ctx.db.prepare('SELECT COUNT(*) AS n FROM sessions').get() as { n: number };
  return Number(r.n);
}

export function listSessions(ctx: StoreContext, limit: number): SessionMeta[] {
  const rows = ctx.db
    .prepare(
      `SELECT s.*, COALESCE(d.name, '') AS device_name
       FROM sessions s
       LEFT JOIN devices d ON d.device_id = s.device_id
       ORDER BY s.updated_at DESC LIMIT ?`,
    )
    .all(limit) as unknown as SessionRow[];
  return rows.map(toMeta);
}

export function getSession(ctx: StoreContext, sessionId: string): SessionMeta | null {
  if (!isSessionId(sessionId)) return null;
  const row = ctx.db
    .prepare(
      `SELECT s.*, COALESCE(d.name, '') AS device_name
       FROM sessions s
       LEFT JOIN devices d ON d.device_id = s.device_id
       WHERE s.session_id = ?`,
    )
    .get(sessionId) as SessionRow | undefined;
  return row ? toMeta(row) : null;
}

export function ownerDeviceId(ctx: StoreContext, sessionId: string): string | null {
  const row = getRow(ctx, sessionId);
  return row?.device_id ? row.device_id : null;
}

/**
 * Moves a session to `to`, under `projectPath` (the worktree the target installed it in) on `branch`, and returns the
 * new owner epoch, or null when the session is no longer as the handoff saw it: still owned by `from`, with
 * the cursor at `byteSize` and the owner epoch at `epoch`. The check and the change are one statement, so
 * nothing can slip in between.
 */
export function handoffOwner(
  ctx: StoreContext,
  sessionId: string,
  from: string,
  to: string,
  byteSize: number,
  epoch: number,
  projectPath: string,
  branch: string,
): number | null {
  if (!isSessionId(sessionId)) return null;
  const result = ctx.db
    .prepare(
      `UPDATE sessions SET device_id = ?, project_path = ?, git_branch = ?, owner_epoch = owner_epoch + 1
       WHERE session_id = ? AND device_id = ? AND byte_size = ? AND owner_epoch = ?`,
    )
    .run(to, projectPath, branch, sessionId, from, byteSize, epoch);
  return Number(result.changes) === 1 ? epoch + 1 : null;
}

/** Lines of chunk `seq` as of `row`: a sealed chunk is full, the open one holds what was committed. */
export function committedLines(row: SessionRow, seq: number): number {
  return seq < row.chunk_count - 1 ? CHUNK_LINES : row.cur_chunk_lines;
}

/** What the user did to a session in Hoplet; a field is absent when nothing was done. */
export interface SessionStateRecord {
  titleOverride?: string;
  pinnedAt?: number;
  archivedAt?: number;
}

/** A change to it: `undefined` leaves a field as it is, `null` clears it. */
export interface SessionStateChange {
  titleOverride?: string | null;
  pinnedAt?: number | null;
  archivedAt?: number | null;
}

function stateOf(
  row: Pick<SessionRow, 'title_override' | 'pinned_at' | 'archived_at'>,
): SessionStateRecord {
  return {
    ...(row.title_override ? { titleOverride: row.title_override } : {}),
    ...(row.pinned_at !== null && row.pinned_at !== undefined
      ? { pinnedAt: Number(row.pinned_at) }
      : {}),
    ...(row.archived_at !== null && row.archived_at !== undefined
      ? { archivedAt: Number(row.archived_at) }
      : {}),
  };
}

/** The user's changes of every session that has any. */
export function sessionStates(ctx: StoreContext): Map<string, SessionStateRecord> {
  const rows = ctx.db
    .prepare(
      `SELECT session_id, title_override, pinned_at, archived_at FROM sessions
       WHERE title_override IS NOT NULL OR pinned_at IS NOT NULL OR archived_at IS NOT NULL`,
    )
    .all() as unknown as SessionRow[];
  return new Map(rows.map((r) => [r.session_id, stateOf(r)]));
}

export function sessionState(ctx: StoreContext, sessionId: string): SessionStateRecord | null {
  const row = getRow(ctx, sessionId);
  return row ? stateOf(row) : null;
}

/**
 * Applies `change` to a session and returns its state after, or null when there is no such session. Only
 * the three state columns are written: the lines, the cursor and the vendor's title are not touched.
 */
export function updateSessionState(
  ctx: StoreContext,
  sessionId: string,
  change: SessionStateChange,
): SessionStateRecord | null {
  const row = getRow(ctx, sessionId);
  if (!row) return null;
  const pick = <T>(next: T | null | undefined, current: T | null): T | null =>
    next === undefined ? current : next;
  const title = pick(change.titleOverride, row.title_override);
  ctx.db
    .prepare(
      'UPDATE sessions SET title_override = ?, pinned_at = ?, archived_at = ? WHERE session_id = ?',
    )
    .run(
      title ? title : null,
      pick(change.pinnedAt, row.pinned_at),
      pick(change.archivedAt, row.archived_at),
      sessionId,
    );
  return sessionState(ctx, sessionId);
}
