// The store is the only module that touches the data directory (docs/architecture.md#boundaries).
// One file per concern: chunks (append, reads), recovery, sessions, devices, search.
import { mkdirSync } from 'node:fs';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import type { Command, SessionMeta, SnapshotChanges } from '@hoplet/shared';
import { migrate } from '../migrations/index.ts';
import {
  type AppendInput,
  type AppendResult,
  append,
  NotOwnerError,
  type RawLinesPage,
  readLine,
  readLines,
  readStream,
  streamFingerprint,
} from './chunks.ts';
import * as commands from './commands.ts';
import * as devices from './devices.ts';
import type { StoreContext } from './layout.ts';
import { recoverOnStart } from './recovery.ts';
import { type RawGlobalSearchResult, type RawSearchResult, search, searchAll } from './search.ts';
import * as sessions from './sessions.ts';
import * as snapshotRead from './snapshot-read.ts';
import * as snapshots from './snapshots.ts';

export type { SessionStateChange, SessionStateRecord } from './sessions.ts';
export { type SnapshotDiffResult, SnapshotReadError } from './snapshot-read.ts';
export { type SnapshotChunk, SnapshotError, type SnapshotRecord } from './snapshots.ts';
export { summarizeTitle } from './title.ts';
export type { AppendInput, AppendResult };
export { NotOwnerError };

export class Store {
  private ctx: StoreContext;

  constructor(dataDir: string) {
    const dir = path.resolve(dataDir);
    mkdirSync(path.join(dir, 'sessions'), { recursive: true });
    const db = new DatabaseSync(path.join(dir, 'hoplet.db'));
    this.ctx = {
      db,
      dataDir: dir,
      compressing: new Set(),
      background: new Set(),
      uploads: new Map(),
      snapshotViews: new Map(),
    };
    try {
      db.exec('PRAGMA synchronous=FULL');
      migrate(db);
      recoverOnStart(this.ctx);
      snapshots.recoverSnapshots(this.ctx);
    } catch (err) {
      db.close();
      throw err;
    }
  }

  /** Resolves when the compression an append started has finished. */
  async idle(): Promise<void> {
    while (this.ctx.background.size > 0) await Promise.all([...this.ctx.background]);
  }

  /** Remembers the commands a session offered in `cwd` of `deviceId`, for drafts of that directory. */
  putDirectoryCommands(deviceId: string, cwd: string, list: Command[]): void {
    commands.putDirectoryCommands(this.ctx, deviceId, cwd, list);
  }

  directoryCommands(deviceId: string, cwd: string): Command[] | null {
    return commands.directoryCommands(this.ctx, deviceId, cwd);
  }

  sessionCount(): number {
    return sessions.sessionCount(this.ctx);
  }

  listSessions(limit = 200): SessionMeta[] {
    return sessions.listSessions(this.ctx, limit);
  }

  getSession(sessionId: string): SessionMeta | null {
    return sessions.getSession(this.ctx, sessionId);
  }

  /** The user's changes of every session that has any: a title of their own, a pin, an archive. */
  sessionStates(): Map<string, sessions.SessionStateRecord> {
    return sessions.sessionStates(this.ctx);
  }

  sessionState(sessionId: string): sessions.SessionStateRecord | null {
    return sessions.sessionState(this.ctx, sessionId);
  }

  /** Changes them and returns the state after; null when there is no such session. */
  updateSessionState(
    sessionId: string,
    change: sessions.SessionStateChange,
  ): sessions.SessionStateRecord | null {
    return sessions.updateSessionState(this.ctx, sessionId, change);
  }

  ownerDeviceId(sessionId: string): string | null {
    return sessions.ownerDeviceId(this.ctx, sessionId);
  }

  /**
   * The handoff commit: owner `from` becomes `to`, the session's project becomes `projectPath` (the target's
   * worktree) on `branch` and the epoch increases, provided the session still has the cursor `byteSize` and the epoch
   * `epoch`. Returns the new epoch, or null when anything differs.
   */
  handoffOwner(
    sessionId: string,
    from: string,
    to: string,
    byteSize: number,
    epoch: number,
    projectPath: string,
    branch: string,
  ): number | null {
    return sessions.handoffOwner(
      this.ctx,
      sessionId,
      from,
      to,
      byteSize,
      epoch,
      projectPath,
      branch,
    );
  }

  /**
   * Receives one chunk of a snapshot upload; returns the record when it completed the snapshot, which then
   * replaces the session's previous one. Throws SnapshotError for anything it refuses.
   */
  putSnapshotChunk(chunk: snapshots.SnapshotChunk): snapshots.SnapshotRecord | null {
    return snapshots.putSnapshotChunk(this.ctx, chunk);
  }

  latestSnapshot(sessionId: string): snapshots.SnapshotRecord | null {
    return snapshots.latestSnapshot(this.ctx, sessionId);
  }

  /** The stored snapshot `snapshotId` in pieces of at most 1 MiB. */
  readSnapshot(sessionId: string, snapshotId: string): AsyncGenerator<Buffer> {
    return snapshots.readSnapshot(this.ctx, sessionId, snapshotId);
  }

  /**
   * The changes of the session's latest snapshot against its base commit, paths relative to the session's
   * working directory; null when the session has no snapshot. Throws SnapshotReadError for a damaged archive.
   */
  snapshotChanges(sessionId: string): Promise<SnapshotChanges | null> {
    return snapshotRead.snapshotChanges(this.ctx, sessionId);
  }

  /** The diff of one changed path of the session's latest snapshot. */
  snapshotDiff(sessionId: string, path: string): Promise<snapshotRead.SnapshotDiffResult> {
    return snapshotRead.snapshotDiff(this.ctx, sessionId, path);
  }

  upsertDevice(deviceId: string, name: string, version: string): void {
    devices.upsertDevice(this.ctx, deviceId, name, version);
  }

  touchDevice(deviceId: string): void {
    devices.touchDevice(this.ctx, deviceId);
  }

  listDevices(): { deviceId: string; name: string; lastSeen?: number }[] {
    return devices.listDevices(this.ctx);
  }

  mintDevice(name: string, deviceId?: string): { deviceId: string; token: string; name: string } {
    return devices.mintDevice(this.ctx, name, deviceId);
  }

  deviceIdForToken(token: string): string | null {
    return devices.deviceIdForToken(this.ctx, token);
  }

  append(input: AppendInput, rawLines: string[]): AppendResult {
    return append(this.ctx, input, rawLines);
  }

  streamFingerprint(sessionId: string): Promise<{ byteSize: number; streamSha256: string } | null> {
    return streamFingerprint(this.ctx, sessionId);
  }

  /** The first `byteSize` committed bytes of the session in pieces of at most `piece` bytes; `byteSize` must be the cursor. */
  readStream(sessionId: string, byteSize: number, piece: number): AsyncGenerator<Buffer> {
    return readStream(this.ctx, sessionId, byteSize, piece);
  }

  readLines(sessionId: string, start: number, limit: number): Promise<RawLinesPage | null> {
    return readLines(this.ctx, sessionId, start, limit);
  }

  readLine(sessionId: string, lineNo: number): Promise<unknown | null> {
    return readLine(this.ctx, sessionId, lineNo);
  }

  search(
    sessionId: string,
    query: string,
    start: number,
    limit: number,
  ): Promise<RawSearchResult | null> {
    return search(this.ctx, sessionId, query, start, limit);
  }

  searchAll(query: string, start: number, limit: number): Promise<RawGlobalSearchResult> {
    return searchAll(this.ctx, query, start, limit);
  }
}
