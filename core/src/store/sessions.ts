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
 * Moves a session to `to`, under `projectPath` (the worktree the target installed it in), and returns the
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
): number | null {
  if (!isSessionId(sessionId)) return null;
  const result = ctx.db
    .prepare(
      `UPDATE sessions SET device_id = ?, project_path = ?, owner_epoch = owner_epoch + 1
       WHERE session_id = ? AND device_id = ? AND byte_size = ? AND owner_epoch = ?`,
    )
    .run(to, projectPath, sessionId, from, byteSize, epoch);
  return Number(result.changes) === 1 ? epoch + 1 : null;
}

/** Lines of chunk `seq` as of `row`: a sealed chunk is full, the open one holds what was committed. */
export function committedLines(row: SessionRow, seq: number): number {
  return seq < row.chunk_count - 1 ? CHUNK_LINES : row.cur_chunk_lines;
}
