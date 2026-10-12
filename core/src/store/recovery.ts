import {
  closeSync,
  existsSync,
  fstatSync,
  fsyncSync,
  ftruncateSync,
  openSync,
  readFileSync,
  renameSync,
  rmSync,
  unlinkSync,
  writeFileSync,
} from 'node:fs';
import { readFile } from 'node:fs/promises';
import { promisify } from 'node:util';
import { gunzipSync, gzip as gzipCallback, gzipSync } from 'node:zlib';
import { isSessionId } from '@hoplet/shared';
import { chunkPath, type StoreContext, sessionDir, syncDirectory } from './layout.ts';

/** An append in flight: enough to cut the chunk files back to the last committed byte. */
export interface AppendRecovery {
  session_id: string;
  first_seq: number;
  tail_bytes: number;
  last_seq: number;
}

/**
 * Startup recovery, in order: finish queued purges, check that every pending namespace key is valid
 * before any file is touched, undo interrupted appends, then finish queued compression.
 */
export function recoverOnStart(ctx: StoreContext): void {
  purgeQueued(ctx);
  for (const table of ['append_recovery', 'compression_pending']) {
    for (const row of ctx.db.prepare(`SELECT session_id FROM ${table}`).all()) {
      if (!isSessionId(row.session_id)) throw new Error('invalid-session-id');
    }
  }
  for (const work of ctx.db
    .prepare('SELECT * FROM append_recovery')
    .all() as unknown as AppendRecovery[]) {
    undoAppend(ctx, work);
  }
  compressPending(ctx);
}

/**
 * Remove the chunk directories queued by a migration. The index rows are already gone, so a
 * crash here only leaves unreachable files and the queue; the next startup finishes the job.
 */
function purgeQueued(ctx: StoreContext): void {
  const queued = ctx.db.prepare('SELECT session_id FROM session_purge').all() as {
    session_id: string;
  }[];
  for (const { session_id: id } of queued) {
    // An id that is not a valid key never named a directory, so there is nothing to remove.
    if (isSessionId(id)) rmSync(sessionDir(ctx, id), { recursive: true, force: true });
    ctx.db.prepare('DELETE FROM session_purge WHERE session_id=?').run(id);
  }
}

export function undoAppend(ctx: StoreContext, work: AppendRecovery): void {
  const dir = sessionDir(ctx, work.session_id);
  const tail = chunkPath(ctx, work.session_id, work.first_seq);
  if (existsSync(tail)) {
    const fd = openSync(tail, 'r+');
    try {
      if (fstatSync(fd).size < work.tail_bytes) throw new Error('committed tail bytes missing');
      ftruncateSync(fd, work.tail_bytes);
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
  } else if (work.tail_bytes !== 0) {
    throw new Error('committed tail missing');
  }
  for (let seq = work.first_seq + 1; seq <= work.last_seq; seq++) {
    const file = chunkPath(ctx, work.session_id, seq);
    if (existsSync(file)) unlinkSync(file);
  }
  syncDirectory(dir);
  ctx.db.prepare('DELETE FROM append_recovery WHERE session_id=?').run(work.session_id);
}

function pendingCompression(ctx: StoreContext, sessionId?: string) {
  return (
    sessionId === undefined
      ? ctx.db.prepare('SELECT * FROM compression_pending').all()
      : ctx.db.prepare('SELECT * FROM compression_pending WHERE session_id=?').all(sessionId)
  ) as { session_id: string; seq: number }[];
}

function doneCompressing(ctx: StoreContext, work: { session_id: string; seq: number }): void {
  ctx.db
    .prepare('DELETE FROM compression_pending WHERE session_id=? AND seq=?')
    .run(work.session_id, work.seq);
}

/** Finishes the recorded compression work before the store serves anything (startup only). */
export function compressPending(ctx: StoreContext): void {
  for (const work of pendingCompression(ctx)) {
    try {
      const plain = chunkPath(ctx, work.session_id, work.seq);
      if (existsSync(plain)) {
        publishGzip(ctx, work.session_id, work.seq, gzipSync(readFileSync(plain), { level: 6 }));
      } else {
        verifyPublished(ctx, work.session_id, work.seq);
      }
      doneCompressing(ctx, work);
    } catch {
      // The committed plaintext or published gzip remains readable; retry recorded work later.
    }
  }
}

const gzipAsync = promisify(gzipCallback);

/**
 * Compresses the sealed chunks an append recorded. The compression runs off the event loop; the file
 * steps after it (temporary file, fsync, rename, unlink) are the same as at startup.
 */
export async function compressPendingAsync(ctx: StoreContext, sessionId: string): Promise<void> {
  for (const work of pendingCompression(ctx, sessionId)) {
    const key = `${work.session_id}/${work.seq}`;
    if (ctx.compressing.has(key)) continue;
    ctx.compressing.add(key);
    try {
      const plain = chunkPath(ctx, work.session_id, work.seq);
      if (existsSync(plain)) {
        const gz = await gzipAsync(await readFile(plain), { level: 6 });
        publishGzip(ctx, work.session_id, work.seq, gz);
      } else {
        verifyPublished(ctx, work.session_id, work.seq);
      }
      doneCompressing(ctx, work);
    } catch {
      // The committed plaintext or published gzip remains readable; retry recorded work later.
    } finally {
      ctx.compressing.delete(key);
    }
  }
}

function publishGzip(ctx: StoreContext, sessionId: string, seq: number, gz: Buffer): void {
  const plain = chunkPath(ctx, sessionId, seq);
  const target = `${plain}.gz`;
  const temp = `${target}.tmp`;
  const dir = sessionDir(ctx, sessionId);
  writeFileSync(temp, gz);
  const fd = openSync(temp, 'r');
  try {
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
  renameSync(temp, target);
  syncDirectory(dir);
  unlinkSync(plain);
  syncDirectory(dir);
}

/** A crash after unlink leaves the published copy and the work record. */
function verifyPublished(ctx: StoreContext, sessionId: string, seq: number): void {
  const target = `${chunkPath(ctx, sessionId, seq)}.gz`;
  gunzipSync(readFileSync(target));
  if (existsSync(`${target}.tmp`)) unlinkSync(`${target}.tmp`);
  syncDirectory(sessionDir(ctx, sessionId));
}
