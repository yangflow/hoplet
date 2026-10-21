import { createHash } from 'node:crypto';
import {
  closeSync,
  existsSync,
  fsyncSync,
  mkdirSync,
  openSync,
  statSync,
  writeSync,
} from 'node:fs';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { promisify } from 'node:util';
import { gunzip as gunzipCallback } from 'node:zlib';
import { isSessionId, type Provider } from '@hoplet/shared';
import { CHUNK_LINES, chunkPath, type StoreContext, sessionDir, syncDirectory } from './layout.ts';
import { type AppendRecovery, compressPendingAsync, undoAppend } from './recovery.ts';
import { committedLines, getRow, type SessionRow } from './sessions.ts';
import { extractMeta } from './title.ts';
import { extractUsage, upsertUsage } from './usage-lines.ts';

const gunzip = promisify(gunzipCallback);

export interface AppendResult {
  byteSize: number;
  lineCount: number;
  appendedLines: number;
  isNew: boolean;
}

export class NotOwnerError extends Error {
  ownerDeviceId: string;
  constructor(ownerDeviceId: string) {
    super(`session belongs to device "${ownerDeviceId}"`);
    this.ownerDeviceId = ownerDeviceId;
  }
}

export interface AppendInput {
  sessionId: string;
  projectDir: string;
  filePath: string;
  deviceId: string;
  /** The vendor of the lines. It is stored when the session is created and never changes. */
  provider: Provider;
  /** Agent PeekTitle; preferred over batch extractMeta when non-empty. */
  title?: string;
  /** The provider configuration the agent says the session belongs to (ADR 0028); absent is the vendor's own. */
  configuration?: string;
}

/**
 * Append raw JSONL lines. The caller guarantees complete lines (no truncation across messages).
 * Lines go to the current chunk as plain text; at 500 lines the chunk rolls over and is compressed
 * to a single-member gzip (c-<n>.ndjson.gz).
 */
export function append(ctx: StoreContext, input: AppendInput, rawLines: string[]): AppendResult {
  const { db } = ctx;
  if (!isSessionId(input.sessionId)) throw new Error('invalid-session-id');
  // Verbatim append, no filtering: the byte cursor (byteSize) must equal the device-side file offset
  // so a resync can seek straight to it (docs/architecture.md#data-flow).
  const lines = rawLines;
  let row = getRow(ctx, input.sessionId);
  if (row?.device_id && row.device_id !== input.deviceId) {
    throw new NotOwnerError(row.device_id);
  }
  const peekTitle = typeof input.title === 'string' ? input.title.trim() : '';
  // The owner agent's word is final, also when it says nothing: the vendor's own configuration.
  const configuration = input.configuration ?? '';
  if (lines.length === 0) {
    if (row) {
      if (configuration !== row.configuration) {
        db.prepare(`UPDATE sessions SET configuration=? WHERE session_id=?`).run(
          configuration,
          input.sessionId,
        );
      }
      if (peekTitle && peekTitle !== row.title) {
        db.prepare(`UPDATE sessions SET title=?, updated_at=? WHERE session_id=?`).run(
          peekTitle,
          Date.now(),
          input.sessionId,
        );
      }
      return {
        byteSize: row.byte_size,
        lineCount: row.line_count,
        appendedLines: 0,
        isNew: false,
      };
    }
    return { byteSize: 0, lineCount: 0, appendedLines: 0, isNew: false };
  }

  const isNew = !row;
  const now = Date.now();
  // What the lines say: project path and title prefer them, a new session's start time may come from them.
  const provider = row ? (row.provider === 'codex' ? 'codex' : 'claude') : input.provider;
  const found = extractMeta(provider, lines);
  // What the lines say they used, recorded in the same transaction as the lines themselves.
  const used = extractUsage(provider, lines, row?.usage_model ?? '', found.createdAt || now);
  if (!row) {
    const createdAt = found.createdAt || now;
    db.prepare(
      `INSERT INTO sessions(session_id, project_dir, provider, created_at, updated_at, chunk_count, device_id, configuration)
       VALUES(?, ?, ?, ?, ?, 1, ?, ?)`,
    ).run(
      input.sessionId,
      input.projectDir,
      input.provider,
      createdAt,
      now,
      input.deviceId,
      configuration,
    );
    const created = getRow(ctx, input.sessionId);
    if (!created) throw new Error(`session row for ${input.sessionId} vanished after insert`);
    row = created;
  }

  // Title prefers agent PeekTitle, then batch ai-title / heuristic.
  const projectPath = row.project_path || found.projectPath;
  const title = peekTitle || found.aiTitle || row.title || found.title;
  const gitBranch = found.gitBranch || row.git_branch;

  const dir = sessionDir(ctx, input.sessionId);
  mkdirSync(dir, { recursive: true });
  syncDirectory(path.join(ctx.dataDir, 'sessions'));
  let seq = row.chunk_count - 1;
  let curLines = row.cur_chunk_lines;
  let chunkCount = row.chunk_count;
  let lineCount = row.line_count;
  let byteSize = row.byte_size;

  const firstSeq = seq;
  const plain = chunkPath(ctx, input.sessionId, seq);
  if (existsSync(`${plain}.gz`)) throw new Error('open chunk unexpectedly compressed');
  const work: AppendRecovery = {
    session_id: input.sessionId,
    first_seq: seq,
    tail_bytes: existsSync(plain) ? statSync(plain).size : 0,
    last_seq: seq + Math.floor((curLines + lines.length - 1) / CHUNK_LINES),
  };
  db.prepare('INSERT INTO append_recovery VALUES(?, ?, ?, ?)').run(
    work.session_id,
    work.first_seq,
    work.tail_bytes,
    work.last_seq,
  );
  let idx = 0;
  try {
    while (idx < lines.length) {
      if (curLines >= CHUNK_LINES) {
        chunkCount++;
        curLines = 0;
        seq = chunkCount - 1;
      }
      const take = Math.min(lines.length - idx, CHUNK_LINES - curLines);
      const seg = lines.slice(idx, idx + take);
      const buf = Buffer.from(`${seg.join('\n')}\n`, 'utf8');
      const fd = openSync(chunkPath(ctx, input.sessionId, seq), 'a');
      try {
        let written = 0;
        while (written < buf.length) {
          const n = writeSync(fd, buf, written, buf.length - written);
          if (n === 0) throw new Error('chunk write made no progress');
          written += n;
        }
        fsyncSync(fd);
      } finally {
        closeSync(fd);
      }
      curLines += take;
      lineCount += take;
      byteSize += buf.length;
      idx += take;
    }
    if (curLines >= CHUNK_LINES) {
      chunkCount++;
      curLines = 0;
    }

    syncDirectory(dir);
    db.exec('BEGIN IMMEDIATE');
    db.prepare(
      `UPDATE sessions SET project_path=?, title=?, git_branch=?, configuration=?, updated_at=?, line_count=?, byte_size=?,
       chunk_count=?, cur_chunk_lines=?, usage_model=? WHERE session_id=?`,
    ).run(
      projectPath,
      title,
      gitBranch,
      configuration,
      now,
      lineCount,
      byteSize,
      chunkCount,
      curLines,
      used.model,
      input.sessionId,
    );
    upsertUsage(db, input.sessionId, used.records);

    for (let sealed = firstSeq; sealed < chunkCount - 1; sealed++) {
      db.prepare('INSERT OR IGNORE INTO compression_pending VALUES(?, ?)').run(
        input.sessionId,
        sealed,
      );
    }
    db.prepare('DELETE FROM append_recovery WHERE session_id=?').run(input.sessionId);
    db.exec('COMMIT');
  } catch (err) {
    try {
      if (db.isTransaction) db.exec('ROLLBACK');
      if (
        db.prepare('SELECT session_id FROM append_recovery WHERE session_id=?').get(input.sessionId)
      ) {
        undoAppend(ctx, work);
      }
    } catch (recoveryError) {
      db.close();
      throw recoveryError;
    }
    throw err;
  }
  // Sealed chunks are compressed off the event loop; they stay readable as plain files until then.
  const compression: Promise<void> = compressPendingAsync(ctx, input.sessionId)
    .catch(() => {})
    .finally(() => ctx.background.delete(compression));
  ctx.background.add(compression);
  return { byteSize, lineCount, appendedLines: lines.length, isNew };
}

/**
 * Hash raw committed bytes, never the parsed REST representation. Chunks are read and hashed one at a
 * time with the event loop free in between.
 */
export async function streamFingerprint(
  ctx: StoreContext,
  sessionId: string,
): Promise<{ byteSize: number; streamSha256: string } | null> {
  const row = getRow(ctx, sessionId);
  if (!row) return null;
  const hash = createHash('sha256');
  let byteSize = 0;
  for (let seq = 0; seq < row.chunk_count; seq++) {
    const bytes = await readChunkBytes(ctx, sessionId, seq);
    if (!bytes) throw new Error('stream-size-mismatch');
    byteSize += bytes.length;
    hash.update(bytes);
    await yieldToEventLoop();
  }
  if (byteSize !== row.byte_size) throw new Error('stream-size-mismatch');
  return { byteSize, streamSha256: hash.digest('hex') };
}

/**
 * The first `byteSize` bytes of a session in pieces of at most `piece` bytes, in order, read chunk by chunk
 * with the event loop free in between. `byteSize` must be the cursor when it starts.
 */
export async function* readStream(
  ctx: StoreContext,
  sessionId: string,
  byteSize: number,
  piece: number,
): AsyncGenerator<Buffer> {
  const row = getRow(ctx, sessionId);
  if (!row || row.byte_size !== byteSize) throw new Error('stream-changed');
  let remaining = byteSize;
  for (let seq = 0; seq < row.chunk_count && remaining > 0; seq++) {
    const bytes = await readChunkBytes(ctx, sessionId, seq);
    if (!bytes) throw new Error('stream-size-mismatch');
    // The open chunk may hold bytes appended after the cursor was read.
    const usable = bytes.subarray(0, Math.min(bytes.length, remaining));
    for (let at = 0; at < usable.length; at += piece) {
      yield usable.subarray(at, Math.min(at + piece, usable.length));
    }
    remaining -= usable.length;
    await yieldToEventLoop();
  }
  if (remaining !== 0) throw new Error('stream-size-mismatch');
}

export function parseLine(raw: string): unknown {
  try {
    return JSON.parse(raw);
  } catch {
    return { raw, _parseError: true };
  }
}

/** A page of stored lines, parsed but not projected: the API maps them onto the conversation model. */
export interface RawLinesPage {
  sessionId: string;
  start: number;
  end: number;
  total: number;
  lines: unknown[];
}

/** One parsed stored line, or null when the session or the line does not exist. */
export async function readLine(
  ctx: StoreContext,
  sessionId: string,
  lineNo: number,
): Promise<unknown | null> {
  const row = getRow(ctx, sessionId);
  if (!row || lineNo < 0 || lineNo >= row.line_count) return null;
  const seq = Math.floor(lineNo / CHUNK_LINES);
  const raw = (await readChunkLines(ctx, row, seq))[lineNo - seq * CHUNK_LINES];
  return raw === undefined ? null : parseLine(raw);
}

/** Line numbers start at 0; a negative start counts from the tail (-N = last N lines). */
export async function readLines(
  ctx: StoreContext,
  sessionId: string,
  startArg: number,
  limit: number,
): Promise<RawLinesPage | null> {
  const row = getRow(ctx, sessionId);
  if (!row) return null;
  const total = row.line_count;
  const start =
    startArg < 0 ? Math.max(0, total + startArg) : Math.min(Math.max(0, startArg), total);
  const end = Math.min(start + limit, total);
  const lines: unknown[] = [];
  let lineNo = start;
  while (lineNo < end) {
    const seq = Math.floor(lineNo / CHUNK_LINES);
    const offsetInChunk = lineNo - seq * CHUNK_LINES;
    const need = Math.min(end - lineNo, CHUNK_LINES - offsetInChunk);
    const chunkLines = await readChunkLines(ctx, row, seq);
    for (let i = 0; i < need; i++) lines.push(parseLine(chunkLines[offsetInChunk + i] ?? ''));
    lineNo += need;
  }
  return { sessionId, start, end, total, lines };
}

/**
 * The bytes of chunk `seq`, plain or compressed, read without blocking the event loop. Compression may
 * replace the plain file by its gzip between two reads, so a missing file is retried as the other form.
 */
export async function readChunkBytes(
  ctx: StoreContext,
  sessionId: string,
  seq: number,
): Promise<Buffer | null> {
  const plain = chunkPath(ctx, sessionId, seq);
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      return await readFile(plain);
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== 'ENOENT') throw err;
    }
    try {
      return await gunzip(await readFile(`${plain}.gz`));
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== 'ENOENT') throw err;
    }
  }
  return null;
}

/**
 * The committed lines of chunk `seq` of `row`: appends after the row was read may already be in the
 * open chunk and are not part of it.
 */
export async function readChunkLines(
  ctx: StoreContext,
  row: SessionRow,
  seq: number,
): Promise<string[]> {
  const bytes = await readChunkBytes(ctx, row.session_id, seq);
  if (!bytes) return [];
  return splitLines(bytes.toString('utf8'), committedLines(row, seq));
}

export function splitLines(text: string, committed: number): string[] {
  const lines = text.split('\n');
  if (lines.length > 0 && lines[lines.length - 1] === '') lines.pop();
  if (lines.length > committed) lines.length = committed;
  return lines;
}

/** Hands the thread back so requests that arrived meanwhile are served before the next chunk. */
export function yieldToEventLoop(): Promise<void> {
  return new Promise((resolve) => setImmediate(resolve));
}
