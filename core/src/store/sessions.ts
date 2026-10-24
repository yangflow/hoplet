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
  configuration: string;
  title_override: string | null;
  pinned_at: number | null;
  archived_at: number | null;
  /** 1 once the usage of the session's earlier lines is recorded, 0 while the backfill has not read them. */
  usage_backfilled: number;
  /** The model a Codex rollout's latest `turn_context` named; the token counts that follow are its. */
  usage_model: string;
  device_name?: string;
}

/** A deleted session is invisible: its row stays only until the deletion has removed the core copy (ADR 0023). */
const NOT_DELETED =
  'NOT EXISTS (SELECT 1 FROM session_tombstones t WHERE t.session_id = s.session_id)';

export function getRow(ctx: StoreContext, sessionId: string): SessionRow | undefined {
  if (!isSessionId(sessionId)) return undefined;
  return ctx.db
    .prepare(`SELECT s.* FROM sessions s WHERE s.session_id = ? AND ${NOT_DELETED}`)
    .get(sessionId) as SessionRow | undefined;
}

/** The row of a session whether or not it is deleted; only the deletion itself reads it. */
export function getRowAny(ctx: StoreContext, sessionId: string): SessionRow | undefined {
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
  const r = ctx.db.prepare(`SELECT COUNT(*) AS n FROM sessions s WHERE ${NOT_DELETED}`).get() as {
    n: number;
  };
  return Number(r.n);
}

export function listSessions(ctx: StoreContext, limit: number): SessionMeta[] {
  const rows = ctx.db
    .prepare(
      `SELECT s.*, COALESCE(d.name, '') AS device_name
       FROM sessions s
       LEFT JOIN devices d ON d.device_id = s.device_id
       WHERE ${NOT_DELETED}
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
       WHERE s.session_id = ? AND ${NOT_DELETED}`,
    )
    .get(sessionId) as SessionRow | undefined;
  return row ? toMeta(row) : null;
}

/** The provider configuration the session's owner announced; empty for the vendor's own (ADR 0028). */
export function sessionConfiguration(ctx: StoreContext, sessionId: string): string {
  return getRow(ctx, sessionId)?.configuration ?? '';
}

export function ownerDeviceId(ctx: StoreContext, sessionId: string): string | null {
  const row = getRow(ctx, sessionId);
  return row?.device_id ? row.device_id : null;
}

/**
 * Moves a session to `to`, under `projectPath` (the worktree the target installed it in) on `branch`, and returns the
 * new owner epoch (and records the `clone` the target installed in, when it created a worktree), or null when the session is no longer as the handoff saw it: still owned by `from`, with
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
  clone: string | null,
  now: number,
  configuration = '',
): number | null {
  if (!isSessionId(sessionId)) return null;
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const result = ctx.db
      .prepare(
        `UPDATE sessions SET device_id = ?, project_path = ?, git_branch = ?, configuration = ?, owner_epoch = owner_epoch + 1
         WHERE session_id = ? AND device_id = ? AND byte_size = ? AND owner_epoch = ?
           AND NOT EXISTS (SELECT 1 FROM session_tombstones t WHERE t.session_id = ?)`,
      )
      .run(to, projectPath, branch, configuration, sessionId, from, byteSize, epoch, sessionId);
    if (Number(result.changes) !== 1) {
      ctx.db.exec('ROLLBACK');
      return null;
    }
    // The worktree the target made for the session, for its deletion later (ADR 0023).
    if (clone) {
      ctx.db
        .prepare(
          `INSERT OR REPLACE INTO session_worktrees(session_id, device_id, from_device_id, clone, worktree, branch, created_at)
           VALUES(?, ?, ?, ?, ?, ?, ?)`,
        )
        .run(sessionId, to, from, clone, projectPath, branch, now);
    } else {
      ctx.db.prepare('DELETE FROM session_worktrees WHERE session_id = ?').run(sessionId);
    }
    ctx.db.exec('COMMIT');
    return epoch + 1;
  } catch (err) {
    ctx.db.exec('ROLLBACK');
    throw err;
  }
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
  /** Its tags, sorted; absent when it has none. */
  tags?: string[];
}

/**
 * A change to it: `undefined` leaves a field as it is, `null` clears it. `addTags` and `removeTags` hold
 * normalized tags (lowercase); removals apply first, a tag the session has or lacks already is a no-op.
 */
export interface SessionStateChange {
  titleOverride?: string | null;
  pinnedAt?: number | null;
  archivedAt?: number | null;
  addTags?: readonly string[];
  removeTags?: readonly string[];
}

function stateOf(
  row: Pick<SessionRow, 'title_override' | 'pinned_at' | 'archived_at'>,
  tags: readonly string[],
): SessionStateRecord {
  return {
    ...(row.title_override ? { titleOverride: row.title_override } : {}),
    ...(row.pinned_at !== null && row.pinned_at !== undefined
      ? { pinnedAt: Number(row.pinned_at) }
      : {}),
    ...(row.archived_at !== null && row.archived_at !== undefined
      ? { archivedAt: Number(row.archived_at) }
      : {}),
    ...(tags.length > 0 ? { tags: [...tags] } : {}),
  };
}

function tagsOf(ctx: StoreContext, sessionId: string): string[] {
  return (
    ctx.db
      .prepare('SELECT tag FROM session_tags WHERE session_id = ? ORDER BY tag')
      .all(sessionId) as { tag: string }[]
  ).map((r) => r.tag);
}

/** The user's changes of every session that has any. */
export function sessionStates(ctx: StoreContext): Map<string, SessionStateRecord> {
  const tags = new Map<string, string[]>();
  for (const r of ctx.db
    .prepare('SELECT session_id, tag FROM session_tags ORDER BY session_id, tag')
    .all() as { session_id: string; tag: string }[]) {
    const list = tags.get(r.session_id);
    if (list) list.push(r.tag);
    else tags.set(r.session_id, [r.tag]);
  }
  const rows = ctx.db
    .prepare(
      `SELECT session_id, title_override, pinned_at, archived_at FROM sessions s
       WHERE (title_override IS NOT NULL OR pinned_at IS NOT NULL OR archived_at IS NOT NULL
              OR EXISTS (SELECT 1 FROM session_tags g WHERE g.session_id = s.session_id))
         AND ${NOT_DELETED}`,
    )
    .all() as unknown as SessionRow[];
  return new Map(rows.map((r) => [r.session_id, stateOf(r, tags.get(r.session_id) ?? [])]));
}

export function sessionState(ctx: StoreContext, sessionId: string): SessionStateRecord | null {
  const row = getRow(ctx, sessionId);
  return row ? stateOf(row, tagsOf(ctx, sessionId)) : null;
}

/**
 * Applies `change` to a session and returns its state after, or null when there is no such session. Only
 * the state columns and its tags are written: the lines, the cursor and the vendor's title are not touched.
 */
export function updateSessionState(
  ctx: StoreContext,
  sessionId: string,
  change: SessionStateChange,
  now = Date.now(),
): SessionStateRecord | null {
  const row = getRow(ctx, sessionId);
  if (!row) return null;
  const pick = <T>(next: T | null | undefined, current: T | null): T | null =>
    next === undefined ? current : next;
  const title = pick(change.titleOverride, row.title_override);
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
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
    for (const tag of change.removeTags ?? []) {
      ctx.db
        .prepare('DELETE FROM session_tags WHERE session_id = ? AND tag = ?')
        .run(sessionId, tag);
    }
    for (const tag of change.addTags ?? []) {
      ctx.db
        .prepare('INSERT OR IGNORE INTO session_tags(session_id, tag, added_at) VALUES(?, ?, ?)')
        .run(sessionId, tag, now);
    }
    ctx.db.exec('COMMIT');
  } catch (err) {
    ctx.db.exec('ROLLBACK');
    throw err;
  }
  return sessionState(ctx, sessionId);
}
