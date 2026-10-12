import { createHash } from 'node:crypto';
import {
  appendFileSync,
  closeSync,
  existsSync,
  fsyncSync,
  mkdirSync,
  openSync,
  readdirSync,
  readSync,
  renameSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { open } from 'node:fs/promises';
import path from 'node:path';
import {
  isSessionId,
  OPAQUE_ID_PATTERN,
  SNAPSHOT_CHUNK_BYTES,
  SNAPSHOT_CHUNK_CHARS,
  SNAPSHOT_MAX_BYTES,
} from '@hoplet/shared';
import {
  type SnapshotPromise,
  type StoreContext,
  snapshotDir,
  snapshotsRoot,
  syncDirectory,
} from './layout.ts';

/** The latest workspace snapshot of a session as the index records it. */
export interface SnapshotRecord {
  sessionId: string;
  snapshotId: string;
  createdAt: number;
  baseCommit: string;
  originUrl: string;
  treeHash: string;
  byteSize: number;
  sha256: string;
}

/** One chunk of an upload, as the agent sent it. */
export interface SnapshotChunk extends SnapshotPromise {
  sessionId: string;
  snapshotId: string;
  seq: number;
  last: boolean;
  /** Base64 of the chunk's bytes. */
  data: string;
}

/** An upload the store refuses; `reason` is diagnostic text for the agent. */
export class SnapshotError extends Error {
  readonly reason: string;
  constructor(reason: string) {
    super(reason);
    this.reason = reason;
  }
}

interface SnapshotRow {
  session_id: string;
  snapshot_id: string;
  created_at: number;
  base_commit: string;
  origin_url: string;
  tree_hash: string;
  byte_size: number;
  sha256: string;
}

const BASE64 = /^[A-Za-z0-9+/]*={0,2}$/;
const HEX64 = /^[0-9a-f]{64}$/;
const OBJECT_ID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;

function toRecord(row: SnapshotRow): SnapshotRecord {
  return {
    sessionId: row.session_id,
    snapshotId: row.snapshot_id,
    createdAt: Number(row.created_at),
    baseCommit: row.base_commit,
    originUrl: row.origin_url,
    treeHash: row.tree_hash,
    byteSize: Number(row.byte_size),
    sha256: row.sha256,
  };
}

function finalName(snapshotId: string): string {
  return `${snapshotId}.tar.gz`;
}

function abortUpload(ctx: StoreContext, sessionId: string): void {
  const up = ctx.uploads.get(sessionId);
  if (!up) return;
  ctx.uploads.delete(sessionId);
  rmSync(up.part, { force: true });
}

function checkPromise(p: SnapshotPromise): void {
  if (!Number.isSafeInteger(p.byteSize) || p.byteSize < 1 || p.byteSize > SNAPSHOT_MAX_BYTES) {
    throw new SnapshotError('too-large');
  }
  if (!HEX64.test(p.sha256)) throw new SnapshotError('invalid-checksum');
  if (!OBJECT_ID.test(p.baseCommit) || !OBJECT_ID.test(p.treeHash)) {
    throw new SnapshotError('invalid-object-id');
  }
  // biome-ignore lint/suspicious/noControlCharactersInRegex: control characters are what is refused
  if (/[\x00-\x1f]/.test(p.originUrl)) throw new SnapshotError('invalid-origin');
  if (!Number.isSafeInteger(p.createdAt) || p.createdAt <= 0)
    throw new SnapshotError('invalid-time');
}

function samePromise(a: SnapshotPromise, b: SnapshotPromise): boolean {
  return (
    a.createdAt === b.createdAt &&
    a.baseCommit === b.baseCommit &&
    a.originUrl === b.originUrl &&
    a.treeHash === b.treeHash &&
    a.byteSize === b.byteSize &&
    a.sha256 === b.sha256
  );
}

function decodeChunk(data: string): Buffer {
  // The length is checked before anything is allocated for the decoded bytes.
  if (data.length === 0 || data.length > SNAPSHOT_CHUNK_CHARS || data.length % 4 !== 0) {
    throw new SnapshotError('invalid-chunk');
  }
  if (!BASE64.test(data)) throw new SnapshotError('invalid-chunk');
  const bytes = Buffer.from(data, 'base64');
  if (bytes.length === 0 || bytes.length > SNAPSHOT_CHUNK_BYTES) {
    throw new SnapshotError('invalid-chunk');
  }
  return bytes;
}

/**
 * Receives one chunk of a snapshot upload and returns the record when the chunk completed the snapshot. A
 * snapshot is written beside its final name, checked against the size and SHA-256 its first chunk promised
 * and renamed into place; only then does it replace the session's previous snapshot, in one index
 * transaction. Any violation discards the upload and throws SnapshotError; the previous snapshot stays.
 */
export function putSnapshotChunk(ctx: StoreContext, chunk: SnapshotChunk): SnapshotRecord | null {
  const { sessionId } = chunk;
  if (!isSessionId(sessionId)) throw new SnapshotError('invalid-session-id');
  if (!OPAQUE_ID_PATTERN.test(chunk.snapshotId)) throw new SnapshotError('invalid-snapshot-id');
  const promised: SnapshotPromise = {
    createdAt: chunk.createdAt,
    baseCommit: chunk.baseCommit,
    originUrl: chunk.originUrl,
    treeHash: chunk.treeHash,
    byteSize: chunk.byteSize,
    sha256: chunk.sha256,
  };
  try {
    const bytes = decodeChunk(chunk.data);
    let up = ctx.uploads.get(sessionId);
    if (chunk.seq === 0) {
      abortUpload(ctx, sessionId);
      checkPromise(promised);
      const dir = snapshotDir(ctx, sessionId);
      mkdirSync(dir, { recursive: true });
      const part = path.join(dir, `.${chunk.snapshotId}.part`);
      writeFileSync(part, '');
      up = {
        snapshotId: chunk.snapshotId,
        part,
        nextSeq: 0,
        received: 0,
        hash: createHash('sha256'),
        promised,
      };
      ctx.uploads.set(sessionId, up);
    } else if (
      !up ||
      up.snapshotId !== chunk.snapshotId ||
      up.nextSeq !== chunk.seq ||
      !samePromise(up.promised, promised)
    ) {
      throw new SnapshotError('out-of-order');
    }
    if (up.received + bytes.length > up.promised.byteSize) throw new SnapshotError('too-large');
    appendFileSync(up.part, bytes);
    up.hash.update(bytes);
    up.received += bytes.length;
    up.nextSeq++;
    if (!chunk.last) {
      if (up.received === up.promised.byteSize) throw new SnapshotError('missing-last');
      return null;
    }
    if (up.received !== up.promised.byteSize) throw new SnapshotError('size-mismatch');
    if (up.hash.digest('hex') !== up.promised.sha256) throw new SnapshotError('checksum-mismatch');
    return publish(ctx, sessionId, chunk.snapshotId, up.part, up.promised);
  } catch (err) {
    abortUpload(ctx, sessionId);
    if (err instanceof SnapshotError) throw err;
    throw new SnapshotError('store-failed');
  }
}

function publish(
  ctx: StoreContext,
  sessionId: string,
  snapshotId: string,
  part: string,
  promised: SnapshotPromise,
): SnapshotRecord {
  const head = Buffer.alloc(2);
  const fd = openSync(part, 'r+');
  try {
    readSync(fd, head, 0, 2, 0);
    // gzip magic: the core never opens the archive, so only the container is checked here.
    if (head[0] !== 0x1f || head[1] !== 0x8b) throw new SnapshotError('not-gzip');
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
  const dir = snapshotDir(ctx, sessionId);
  renameSync(part, path.join(dir, finalName(snapshotId)));
  syncDirectory(dir);
  ctx.uploads.delete(sessionId);
  const record: SnapshotRecord = { sessionId, snapshotId, ...promised };
  ctx.db.exec('BEGIN IMMEDIATE');
  try {
    ctx.db
      .prepare(
        `INSERT OR REPLACE INTO snapshots(session_id, snapshot_id, created_at, base_commit, origin_url,
           tree_hash, byte_size, sha256) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
      )
      .run(
        sessionId,
        snapshotId,
        record.createdAt,
        record.baseCommit,
        record.originUrl,
        record.treeHash,
        record.byteSize,
        record.sha256,
      );
    ctx.db.exec('COMMIT');
  } catch (err) {
    ctx.db.exec('ROLLBACK');
    rmSync(path.join(dir, finalName(snapshotId)), { force: true });
    throw err;
  }
  // The index points at the new file; whatever else is in the directory is the previous snapshot.
  for (const name of readdirSync(dir)) {
    if (name !== finalName(snapshotId))
      rmSync(path.join(dir, name), { recursive: true, force: true });
  }
  return record;
}

/** The latest snapshot of a session, or null. */
export function latestSnapshot(ctx: StoreContext, sessionId: string): SnapshotRecord | null {
  if (!isSessionId(sessionId)) return null;
  const row = ctx.db.prepare('SELECT * FROM snapshots WHERE session_id = ?').get(sessionId) as
    | SnapshotRow
    | undefined;
  return row ? toRecord(row) : null;
}

/**
 * The bytes of a stored snapshot in pieces of at most `SNAPSHOT_CHUNK_BYTES`, in order. It reads the file
 * the index names for `snapshotId`; when a newer snapshot replaced it meanwhile, the read fails.
 */
export async function* readSnapshot(
  ctx: StoreContext,
  sessionId: string,
  snapshotId: string,
): AsyncGenerator<Buffer> {
  const record = latestSnapshot(ctx, sessionId);
  if (!record || record.snapshotId !== snapshotId) throw new Error('snapshot-replaced');
  const file = await open(path.join(snapshotDir(ctx, sessionId), finalName(snapshotId)), 'r');
  try {
    let offset = 0;
    while (offset < record.byteSize) {
      const buffer = Buffer.alloc(Math.min(SNAPSHOT_CHUNK_BYTES, record.byteSize - offset));
      const { bytesRead } = await file.read(buffer, 0, buffer.length, offset);
      if (bytesRead !== buffer.length) throw new Error('snapshot-truncated');
      offset += bytesRead;
      yield buffer;
    }
  } finally {
    await file.close();
  }
}

/**
 * At startup: discards unfinished uploads and every file the index does not name, and forgets snapshots
 * whose file is gone.
 */
export function recoverSnapshots(ctx: StoreContext): void {
  const root = snapshotsRoot(ctx);
  const rows = ctx.db.prepare('SELECT session_id, snapshot_id FROM snapshots').all() as unknown as {
    session_id: string;
    snapshot_id: string;
  }[];
  const wanted = new Map<string, string>();
  for (const row of rows) {
    if (isSessionId(row.session_id)) wanted.set(row.session_id, finalName(row.snapshot_id));
  }
  if (existsSync(root)) {
    for (const entry of readdirSync(root, { withFileTypes: true })) {
      if (!entry.isDirectory() || !isSessionId(entry.name)) continue;
      const dir = path.join(root, entry.name);
      const want = wanted.get(entry.name);
      for (const name of readdirSync(dir)) {
        if (name !== want) rmSync(path.join(dir, name), { recursive: true, force: true });
      }
      if (!want) rmSync(dir, { recursive: true, force: true });
    }
  }
  for (const [sessionId, name] of wanted) {
    if (!existsSync(path.join(snapshotDir(ctx, sessionId), name))) {
      ctx.db.prepare('DELETE FROM snapshots WHERE session_id = ?').run(sessionId);
    }
  }
}
