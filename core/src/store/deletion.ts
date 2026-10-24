import { rmSync } from 'node:fs';
import { isSessionId } from '@hoplet/shared';
import { type StoreContext, sessionDir, snapshotDir } from './layout.ts';
import { dropSessionRules } from './permission-rules.ts';

/** What a handoff created for a session on its target device (ADR 0023). */
export interface WorktreeRecord {
  sessionId: string;
  deviceId: string;
  fromDeviceId: string;
  clone: string;
  worktree: string;
  branch: string;
  createdAt: number;
}

/** A deleted session that ingest refuses. */
export interface Tombstone {
  sessionId: string;
  deviceId: string;
  deletedAt: number;
  forgotten: boolean;
}

export function worktreeOf(ctx: StoreContext, sessionId: string): WorktreeRecord | null {
  const r = ctx.db.prepare('SELECT * FROM session_worktrees WHERE session_id = ?').get(sessionId) as
    | {
        session_id: string;
        device_id: string;
        from_device_id: string;
        clone: string;
        worktree: string;
        branch: string;
        created_at: number;
      }
    | undefined;
  return r
    ? {
        sessionId: r.session_id,
        deviceId: r.device_id,
        fromDeviceId: r.from_device_id,
        clone: r.clone,
        worktree: r.worktree,
        branch: r.branch,
        createdAt: Number(r.created_at),
      }
    : null;
}

/** The worktrees Hoplet made on a device, each with the clone it was made from (ADR 0037). */
export function deviceWorktrees(
  ctx: StoreContext,
  deviceId: string,
): { clone: string; worktree: string }[] {
  return (
    ctx.db
      .prepare(
        'SELECT clone, worktree FROM session_worktrees WHERE device_id = ? ORDER BY worktree',
      )
      .all(deviceId) as { clone: string; worktree: string }[]
  ).map((r) => ({ clone: r.clone, worktree: r.worktree }));
}

/**
 * Records the worktree the agent made for a session it started, so that deleting the session offers it (ADR 0023).
 * The device that made it is also the one that owned the session before, so `from_device_id` is the same.
 */
export function recordNewWorktree(
  ctx: StoreContext,
  sessionId: string,
  deviceId: string,
  worktree: { clone: string; path: string; branch: string },
  now: number,
): void {
  ctx.db
    .prepare(
      `INSERT OR REPLACE INTO session_worktrees(session_id, device_id, from_device_id, clone, worktree, branch, created_at)
       VALUES(?, ?, ?, ?, ?, ?, ?)`,
    )
    .run(sessionId, deviceId, deviceId, worktree.clone, worktree.path, worktree.branch, now);
}

export function tombstoneOf(ctx: StoreContext, sessionId: string): Tombstone | null {
  if (!isSessionId(sessionId)) return null;
  const r = ctx.db
    .prepare('SELECT * FROM session_tombstones WHERE session_id = ?')
    .get(sessionId) as
    | { session_id: string; device_id: string; deleted_at: number; forgotten: number }
    | undefined;
  return r
    ? {
        sessionId: r.session_id,
        deviceId: r.device_id,
        deletedAt: Number(r.deleted_at),
        forgotten: Number(r.forgotten) === 1,
      }
    : null;
}

export function isTombstoned(ctx: StoreContext, sessionId: string): boolean {
  return !!ctx.db.prepare('SELECT 1 FROM session_tombstones WHERE session_id = ?').get(sessionId);
}

/** Writes the tombstone; a repeated call keeps the first one. */
export function tombstone(
  ctx: StoreContext,
  sessionId: string,
  deviceId: string,
  now: number,
): void {
  ctx.db
    .prepare(
      `INSERT INTO session_tombstones(session_id, device_id, deleted_at, forgotten) VALUES(?, ?, ?, 0)
       ON CONFLICT(session_id) DO NOTHING`,
    )
    .run(sessionId, deviceId, now);
}

/** Records that the owner's agent stopped syncing the session. */
export function markForgotten(ctx: StoreContext, sessionId: string): void {
  ctx.db.prepare('UPDATE session_tombstones SET forgotten = 1 WHERE session_id = ?').run(sessionId);
}

/** Removes a tombstone; true when there was one. */
export function clearTombstone(ctx: StoreContext, sessionId: string): boolean {
  return (
    Number(
      ctx.db.prepare('DELETE FROM session_tombstones WHERE session_id = ?').run(sessionId).changes,
    ) > 0
  );
}

/** The tombstones whose device has not confirmed it forgot the session. */
export function unforgotten(ctx: StoreContext, deviceId: string): string[] {
  return (
    ctx.db
      .prepare('SELECT session_id FROM session_tombstones WHERE device_id = ? AND forgotten = 0')
      .all(deviceId) as { session_id: string }[]
  ).map((r) => r.session_id);
}

/**
 * Removes the core's copy of a session, the last step of a deletion: in one transaction every row that belongs to
 * it goes and the ID is queued in `session_purge`, then the session and snapshot directories go and the queue
 * entry with them. A crash between the two leaves the queue entry, which the next startup finishes. The
 * tombstone stays: it is what keeps the session from coming back.
 */
export function purgeSession(ctx: StoreContext, sessionId: string): void {
  if (!isSessionId(sessionId)) throw new Error('invalid-session-id');
  const snapshot = ctx.db
    .prepare('SELECT snapshot_id FROM snapshots WHERE session_id = ?')
    .get(sessionId) as { snapshot_id: string } | undefined;
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    dropSessionRules(ctx, sessionId);
    for (const table of [
      'sessions',
      'snapshots',
      'session_queue',
      'session_queue_state',
      'append_recovery',
      'compression_pending',
      'session_worktrees',
      'usage',
      'audit_cursor',
      'session_tags',
    ]) {
      ctx.db.prepare(`DELETE FROM ${table} WHERE session_id = ?`).run(sessionId);
    }
    ctx.db
      .prepare('DELETE FROM session_continuations WHERE from_session_id = ? OR to_session_id = ?')
      .run(sessionId, sessionId);
    ctx.db.prepare('INSERT OR IGNORE INTO session_purge(session_id) VALUES(?)').run(sessionId);
    ctx.db.exec('COMMIT');
  } catch (err) {
    ctx.db.exec('ROLLBACK');
    throw err;
  }
  ctx.uploads.delete(sessionId);
  if (snapshot) ctx.snapshotViews.delete(snapshot.snapshot_id);
  removePurged(ctx, sessionId);
}

/** Removes the directories of a queued purge, then its queue entry. */
export function removePurged(ctx: StoreContext, sessionId: string): void {
  rmSync(sessionDir(ctx, sessionId), { recursive: true, force: true });
  rmSync(snapshotDir(ctx, sessionId), { recursive: true, force: true });
  ctx.db.prepare('DELETE FROM session_purge WHERE session_id = ?').run(sessionId);
}
