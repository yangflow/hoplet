/**
 * Reads the changes and per-path diffs of a stored workspace snapshot (docs/spec.md#workspace). The archive
 * (docs/architecture.md#workspace-snapshots) is streamed and never held whole: one pass lists the changes, which
 * are cached per snapshot, and a diff is one more pass that stops after the path's part. Paths are relative
 * to the session's working directory (the archive's `subdir`), as the owner's live listing has them.
 */
import { open } from 'node:fs/promises';
import path from 'node:path';
import { pipeline } from 'node:stream';
import { constants, createGunzip, inflateSync } from 'node:zlib';
import type { SnapshotChanges, SnapshotDiff, WorkspaceChange } from '@hoplet/shared';
import { type StoreContext, snapshotDir } from './layout.ts';
import { latestSnapshot, type SnapshotRecord } from './snapshots.ts';

/** The largest diff text an answer carries; beyond it the answer is `truncated` with the full size. */
export const MAX_DIFF_BYTES = 256 * 1024;
/** The archive's uncompressed bound (docs/hosting.md#resources), enforced while decompressing. */
const MAX_UNCOMPRESSED = 256 * 1024 * 1024;
/** Snapshots whose changes stay parsed in memory. */
const CACHED_SNAPSHOTS = 16;
/** How much of one line the diff parser looks at; hunk content beyond it is only counted. */
const LINE_HEAD = 64 * 1024;
/** How much of a file git looks at for a NUL byte to call it binary. */
const BINARY_SNIFF = 8000;

/** A stored snapshot the store cannot read: its archive is damaged. */
export class SnapshotReadError extends Error {}

/** One parsed snapshot: its changes, and where each changed path is in the archive. */
export interface SnapshotView {
  subdir: string;
  /** The URL of the repository's remote `origin`, empty without one. */
  origin: string;
  changes: WorkspaceChange[];
  /** Change by working-directory path, with its name in the archive (relative to the repository). */
  byPath: Map<string, { change: WorkspaceChange; repoPath: string }>;
}

export type SnapshotDiffResult =
  | { ok: true; diff: SnapshotDiff }
  | { ok: false; error: 'no-snapshot' | 'not-found' };

// ---------------------------------------------------------------------------------------------------------
// Reading the archive

/** Bytes of the decompressed archive, consumed front to back, with the uncompressed bound applied. */
class Bytes {
  private buf: Buffer = Buffer.alloc(0);
  private total = 0;
  private readonly it: AsyncIterator<Buffer>;
  constructor(it: AsyncIterator<Buffer>) {
    this.it = it;
  }

  private async fill(): Promise<boolean> {
    const next = await this.it.next();
    if (next.done) return false;
    this.total += next.value.length;
    if (this.total > MAX_UNCOMPRESSED) throw new SnapshotReadError('snapshot-too-large');
    this.buf = this.buf.length === 0 ? next.value : Buffer.concat([this.buf, next.value]);
    return true;
  }

  /** Exactly `n` bytes, or null at a clean end. */
  async exact(n: number): Promise<Buffer | null> {
    while (this.buf.length < n) {
      if (!(await this.fill())) {
        if (this.buf.length === 0) return null;
        throw new SnapshotReadError('snapshot-truncated');
      }
    }
    const out = this.buf.subarray(0, n);
    this.buf = this.buf.subarray(n);
    return out;
  }

  /** Up to `left.n` bytes in pieces as they arrive; `left.n` counts down as they are taken. */
  async *take(left: { n: number }): AsyncGenerator<Buffer> {
    while (left.n > 0) {
      if (this.buf.length === 0 && !(await this.fill())) {
        throw new SnapshotReadError('snapshot-truncated');
      }
      const piece = this.buf.subarray(0, Math.min(left.n, this.buf.length));
      this.buf = this.buf.subarray(piece.length);
      left.n -= piece.length;
      yield piece;
    }
  }

  async skip(n: number): Promise<void> {
    for await (const _ of this.take({ n })) {
      // discarded
    }
  }
}

interface TarEntry {
  name: string;
  /** `file` or `link`; other entry types are not part of a snapshot and are passed over. */
  type: 'file' | 'link' | 'other';
  size: number;
  linkname: string;
}

function cString(b: Buffer, from: number, to: number): string {
  const end = b.indexOf(0, from);
  return b.toString('utf8', from, end < 0 || end > to ? to : end);
}

function octal(b: Buffer, from: number, to: number): number {
  if ((b[from] ?? 0) & 0x80) {
    // base-256: big-endian after the marker bit
    let n = (b[from] ?? 0) & 0x7f;
    for (let i = from + 1; i < to; i++) n = n * 256 + (b[i] ?? 0);
    return n;
  }
  const text = cString(b, from, to).trim();
  return text === '' ? 0 : Number.parseInt(text, 8);
}

function checksumOk(h: Buffer): boolean {
  let sum = 0;
  for (let i = 0; i < 512; i++) sum += i >= 148 && i < 156 ? 32 : (h[i] ?? 0);
  return sum === octal(h, 148, 156);
}

/** The `path` and `linkpath` records of a PAX extended header. */
function paxRecords(data: Buffer): Map<string, string> {
  const out = new Map<string, string>();
  let at = 0;
  while (at < data.length) {
    const space = data.indexOf(0x20, at);
    if (space < 0) break;
    const length = Number.parseInt(data.toString('utf8', at, space), 10);
    if (!Number.isSafeInteger(length) || length <= 0 || at + length > data.length) break;
    const record = data.toString('utf8', space + 1, at + length - 1);
    const eq = record.indexOf('=');
    if (eq > 0) out.set(record.slice(0, eq), record.slice(eq + 1));
    at += length;
  }
  return out;
}

/**
 * Calls `visit` with every entry of a tar stream in order and an iterator of exactly its bytes; whatever
 * `visit` leaves unread is skipped. `visit` returns true to stop the scan.
 */
async function scanTar(
  bytes: Bytes,
  visit: (entry: TarEntry, body: AsyncIterable<Buffer>) => Promise<boolean | undefined>,
): Promise<void> {
  let longName: string | null = null;
  let longLink: string | null = null;
  for (;;) {
    const h = await bytes.exact(512);
    if (h === null || h.every((c) => c === 0)) return;
    if (!checksumOk(h)) throw new SnapshotReadError('snapshot-corrupt');
    const size = octal(h, 124, 136);
    if (!Number.isSafeInteger(size) || size < 0) throw new SnapshotReadError('snapshot-corrupt');
    const padding = (512 - (size % 512)) % 512;
    const flag = String.fromCharCode(h[156] ?? 0);
    if (flag === 'x' || flag === 'L' || flag === 'K') {
      if (size > 1024 * 1024) throw new SnapshotReadError('snapshot-corrupt');
      const data = (await bytes.exact(size)) ?? Buffer.alloc(0);
      await bytes.skip(padding);
      if (flag === 'x') {
        const records = paxRecords(data);
        longName = records.get('path') ?? longName;
        longLink = records.get('linkpath') ?? longLink;
      } else if (flag === 'L') longName = cString(data, 0, data.length);
      else longLink = cString(data, 0, data.length);
      continue;
    }
    let name = cString(h, 0, 100);
    if (h.toString('latin1', 257, 262) === 'ustar') {
      const prefix = cString(h, 345, 500);
      if (prefix !== '') name = `${prefix}/${name}`;
    }
    const entry: TarEntry = {
      name: longName ?? name,
      type: flag === '0' || flag === '\0' ? 'file' : flag === '2' ? 'link' : 'other',
      size,
      linkname: longLink ?? cString(h, 157, 257),
    };
    longName = null;
    longLink = null;
    const left = { n: size };
    const stop = await visit(entry, bytes.take(left));
    if (stop) return;
    await bytes.skip(left.n + padding);
  }
}

/**
 * Opens the latest snapshot of a session and scans it. The file is opened before it is read, so a newer
 * snapshot replacing it meanwhile does not cut the read short; one that replaced it before the open is
 * read instead.
 */
async function withArchive<T>(
  ctx: StoreContext,
  sessionId: string,
  scan: (record: SnapshotRecord, bytes: Bytes) => Promise<T>,
): Promise<{ record: SnapshotRecord; value: T } | null> {
  for (let attempt = 0; ; attempt++) {
    const record = latestSnapshot(ctx, sessionId);
    if (!record) return null;
    let handle: Awaited<ReturnType<typeof open>>;
    try {
      handle = await open(
        path.join(snapshotDir(ctx, sessionId), `${record.snapshotId}.tar.gz`),
        'r',
      );
    } catch (err) {
      if (attempt < 2 && (err as NodeJS.ErrnoException).code === 'ENOENT') continue;
      throw err;
    }
    const source = handle.createReadStream({ highWaterMark: 1024 * 1024 });
    const gunzip = createGunzip();
    pipeline(source, gunzip, () => {});
    try {
      return { record, value: await scan(record, new Bytes(gunzip[Symbol.asyncIterator]())) };
    } catch (err) {
      if ((err as { code?: string }).code?.startsWith('Z_')) {
        throw new SnapshotReadError('snapshot-corrupt');
      }
      throw err;
    } finally {
      gunzip.destroy();
      source.destroy();
      await handle.close().catch(() => {});
    }
  }
}

// ---------------------------------------------------------------------------------------------------------
// The tracked diff

/** Decodes a C-quoted git path (`"a/caf\303\251"`) starting at `from`; returns its bytes and where it ends. */
function unquote(s: string, from: number): { bytes: Buffer; end: number } {
  const out: number[] = [];
  const escapes: Record<string, number> = {
    a: 7,
    b: 8,
    t: 9,
    n: 10,
    v: 11,
    f: 12,
    r: 13,
    '"': 34,
    '\\': 92,
  };
  let i = from + 1;
  while (i < s.length && s[i] !== '"') {
    const c = s[i] as string;
    if (c !== '\\') {
      out.push(...Buffer.from(c, 'utf8'));
      i++;
      continue;
    }
    const next = s[i + 1] ?? '';
    if (/[0-7]/.test(next)) {
      out.push(Number.parseInt(s.slice(i + 1, i + 4), 8) & 0xff);
      i += 4;
    } else {
      out.push(escapes[next] ?? next.charCodeAt(0));
      i += 2;
    }
  }
  return { bytes: Buffer.from(out), end: i + 1 };
}

/** The path of a `diff --git a/<path> b/<path>` line; the diff has no renames, so both sides name it. */
export function diffGitPath(line: string): string | null {
  const rest = line.slice('diff --git '.length);
  let side: string;
  if (rest.startsWith('"')) {
    side = unquote(rest, 0).bytes.toString('utf8');
  } else {
    if ((rest.length - 1) % 2 !== 0) return null;
    side = rest.slice(0, (rest.length - 1) / 2);
  }
  return side.startsWith('a/') ? side.slice(2) : null;
}

/** One file's part of the tracked diff as it is read. */
interface Section {
  repoPath: string;
  status: 'modified' | 'added' | 'deleted';
  binary: boolean;
  inHunks: boolean;
  added: number;
  removed: number;
}

/** The hunks, the binary patch head and the size of the one section a diff read asks for. */
interface Capture {
  repoPath: string;
  section: Section | null;
  kept: Buffer[];
  keptBytes: number;
  size: number;
  truncated: boolean;
  /** The `literal`/`delta` hunks of a binary patch: kind, declared size and their first data lines. */
  patches: { kind: string; size: number; lines: string[] }[];
  done: boolean;
}

/** The first bytes of the lines of a hunk: space, +, -, \ and @. */
const HUNK_LINE = [0x20, 0x2b, 0x2d, 0x5c, 0x40];

/**
 * Splits the tracked diff into lines and follows its sections. Every line ends in `endLine` with its head
 * (at most LINE_HEAD bytes); the hunk lines of the captured section are also kept whole, up to the cap.
 */
class DiffReader {
  readonly sections: Section[] = [];
  private current: Section | null = null;
  private head: Buffer[] = [];
  private headBytes = 0;
  private lineStart = true;
  private capturing = false;
  private readonly capture: Capture | null;

  constructor(capture: Capture | null) {
    this.capture = capture;
  }

  push(chunk: Buffer): void {
    let at = 0;
    while (at < chunk.length && !this.capture?.done) {
      const nl = chunk.indexOf(0x0a, at);
      const end = nl < 0 ? chunk.length : nl + 1;
      this.fragment(chunk.subarray(at, end), nl >= 0);
      at = end;
    }
  }

  end(): void {
    if (!this.lineStart) this.fragment(Buffer.alloc(0), true);
    this.finishSection();
  }

  private fragment(piece: Buffer, endsLine: boolean): void {
    if (this.lineStart && piece.length > 0) {
      // A hunk line starts with a space, +, - or \, and another hunk with @@; anything else ends the hunks.
      const s = this.current;
      if (s?.inHunks && !HUNK_LINE.includes(piece[0] as number)) s.inHunks = false;
      this.capturing = s?.inHunks === true && this.isCaptured(s);
      this.lineStart = false;
    }
    if (this.capturing && this.capture) this.keep(piece);
    if (this.headBytes < LINE_HEAD) {
      const part = piece.subarray(0, LINE_HEAD - this.headBytes);
      this.head.push(part);
      this.headBytes += part.length;
    }
    if (endsLine) {
      const line = Buffer.concat(this.head).toString('utf8').replace(/\n$/, '');
      this.head = [];
      this.headBytes = 0;
      this.lineStart = true;
      this.endLine(line);
    }
  }

  private isCaptured(section: Section): boolean {
    return this.capture !== null && section.repoPath === this.capture.repoPath;
  }

  private keep(piece: Buffer): void {
    const c = this.capture as Capture;
    c.size += piece.length;
    if (c.truncated) return;
    if (c.keptBytes + piece.length > MAX_DIFF_BYTES) {
      c.truncated = true;
      return;
    }
    c.kept.push(piece);
    c.keptBytes += piece.length;
  }

  private finishSection(): void {
    if (!this.current) return;
    this.sections.push(this.current);
    if (this.capture && this.isCaptured(this.current)) this.capture.done = true;
    this.current = null;
  }

  private endLine(line: string): void {
    if (line.startsWith('diff --git ')) {
      this.finishSection();
      const repoPath = diffGitPath(line);
      if (repoPath === null) throw new SnapshotReadError('snapshot-corrupt');
      this.current = {
        repoPath,
        status: 'modified',
        binary: false,
        inHunks: false,
        added: 0,
        removed: 0,
      };
      if (this.capture && this.isCaptured(this.current)) this.capture.section = this.current;
      return;
    }
    const s = this.current;
    if (!s) return;
    if (s.inHunks) {
      if (line.startsWith('+')) s.added++;
      else if (line.startsWith('-')) s.removed++;
      return;
    }
    if (line.startsWith('@@')) {
      s.inHunks = true;
      if (this.isCaptured(s)) {
        // The first hunk header was seen whole only now; keep it and every hunk line from here on.
        this.capturing = true;
        this.keep(Buffer.from(`${line}\n`));
      }
    } else if (line.startsWith('new file mode')) s.status = 'added';
    else if (line.startsWith('deleted file mode')) s.status = 'deleted';
    else if (line.startsWith('GIT binary patch') || line.startsWith('Binary files ')) {
      s.binary = true;
    } else if (s.binary && this.isCaptured(s)) {
      this.binaryLine(line);
    }
  }

  private binaryLine(line: string): void {
    const c = this.capture as Capture;
    const hunk = /^(literal|delta) (\d+)$/.exec(line);
    if (hunk) {
      c.patches.push({ kind: hunk[1] as string, size: Number(hunk[2]), lines: [] });
      return;
    }
    const last = c.patches[c.patches.length - 1];
    if (last && line !== '' && last.lines.length < 8) last.lines.push(line);
  }
}

const BASE85 =
  '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz!#$%&()*+-;<=>?@^_`{|}~';

/** Decodes the data lines of a git binary patch hunk (base85, a length letter per line). */
function base85Lines(lines: string[]): Buffer {
  const out: number[] = [];
  for (const line of lines) {
    const letter = line.charCodeAt(0);
    const length = letter <= 90 ? letter - 64 : letter - 96 + 26;
    const bytes: number[] = [];
    for (let i = 1; i + 5 <= line.length; i += 5) {
      let acc = 0;
      for (let j = 0; j < 5; j++) acc = acc * 85 + BASE85.indexOf(line[i + j] as string);
      bytes.push((acc >>> 24) & 0xff, (acc >>> 16) & 0xff, (acc >>> 8) & 0xff, acc & 0xff);
    }
    out.push(...bytes.slice(0, length));
  }
  return Buffer.from(out);
}

/** The size of the side a binary patch hunk produces: declared for `literal`, read from the delta head. */
function patchResultSize(patch: {
  kind: string;
  size: number;
  lines: string[];
}): number | undefined {
  if (patch.kind === 'literal') return patch.size;
  try {
    const head = inflateSync(base85Lines(patch.lines), { finishFlush: constants.Z_SYNC_FLUSH });
    let at = 0;
    const varint = (): number => {
      let n = 0;
      let shift = 0;
      for (;;) {
        const b = head[at++];
        if (b === undefined) throw new Error('short delta');
        n += (b & 0x7f) * 2 ** shift;
        if (!(b & 0x80)) return n;
        shift += 7;
      }
    };
    varint(); // the source size
    return varint();
  } catch {
    return undefined;
  }
}

// ---------------------------------------------------------------------------------------------------------
// Untracked files

/** Counts lines as git does (a last line without a newline counts) and notes a NUL byte in git's window. */
async function countFile(body: AsyncIterable<Buffer>): Promise<{ lines: number; binary: boolean }> {
  let lines = 0;
  let seen = 0;
  let binary = false;
  let last = 0x0a;
  for await (const piece of body) {
    if (seen < BINARY_SNIFF && piece.subarray(0, BINARY_SNIFF - seen).includes(0)) binary = true;
    seen += piece.length;
    for (let i = piece.indexOf(0x0a); i >= 0; i = piece.indexOf(0x0a, i + 1)) lines++;
    if (piece.length > 0) last = piece[piece.length - 1] as number;
  }
  if (seen > 0 && last !== 0x0a) lines++;
  return { lines, binary };
}

/** The diff git shows for a new file: one hunk of added lines, cut at MAX_DIFF_BYTES. */
async function addedDiff(
  body: AsyncIterable<Buffer>,
  lines: number,
): Promise<{ diff: string; truncated: boolean; size: number }> {
  if (lines === 0) return { diff: '', truncated: false, size: 0 };
  const kept: Buffer[] = [];
  let keptBytes = 0;
  let size = 0;
  let truncated = false;
  const emit = (piece: Buffer): void => {
    size += piece.length;
    if (truncated) return;
    if (keptBytes + piece.length > MAX_DIFF_BYTES) {
      truncated = true;
      return;
    }
    kept.push(piece);
    keptBytes += piece.length;
  };
  const plus = Buffer.from('+');
  emit(Buffer.from(lines === 1 ? '@@ -0,0 +1 @@\n' : `@@ -0,0 +1,${lines} @@\n`));
  let lineStart = true;
  for await (const chunk of body) {
    let at = 0;
    while (at < chunk.length) {
      const nl = chunk.indexOf(0x0a, at);
      const end = nl < 0 ? chunk.length : nl + 1;
      if (lineStart) emit(plus);
      emit(chunk.subarray(at, end));
      lineStart = nl >= 0;
      at = end;
    }
  }
  if (!lineStart) emit(Buffer.from('\n\\ No newline at end of file\n'));
  return { diff: cutAtLine(Buffer.concat(kept), truncated), truncated, size };
}

function cutAtLine(text: Buffer, truncated: boolean): string {
  if (!truncated) return text.toString('utf8');
  const nl = text.lastIndexOf(0x0a);
  return text.toString('utf8', 0, nl < 0 ? text.length : nl + 1);
}

// ---------------------------------------------------------------------------------------------------------
// Changes and diffs

const DIFF_NAME = 'tracked.diff';
const META_NAME = 'snapshot.json';
const FILE_PREFIX = 'untracked/';

/** The working-directory path of a repository path, or null when it is outside the working directory. */
function localOf(subdir: string, repoPath: string): string | null {
  if (subdir === '') return repoPath;
  return repoPath.startsWith(`${subdir}/`) ? repoPath.slice(subdir.length + 1) : null;
}

async function readMeta(body: AsyncIterable<Buffer>): Promise<{ subdir: string; origin: string }> {
  const parts: Buffer[] = [];
  for await (const piece of body) parts.push(piece);
  try {
    const meta = JSON.parse(Buffer.concat(parts).toString('utf8')) as {
      subdir?: unknown;
      originUrl?: unknown;
    };
    return {
      subdir: typeof meta.subdir === 'string' ? meta.subdir : '',
      // A URL's user and password never leave the core; clones are matched by host and path.
      origin:
        typeof meta.originUrl === 'string'
          ? meta.originUrl.replace(/^([a-z][a-z0-9+.-]*:\/\/)[^@/]*@/i, '$1')
          : '',
    };
  } catch {
    throw new SnapshotReadError('snapshot-corrupt');
  }
}

async function parseSnapshot(bytes: Bytes): Promise<SnapshotView> {
  let meta = { subdir: '', origin: '' };
  const entries: { repoPath: string; change: WorkspaceChange }[] = [];
  await scanTar(bytes, async (entry, body) => {
    if (entry.name === META_NAME) {
      meta = await readMeta(body);
    } else if (entry.name === DIFF_NAME) {
      const reader = new DiffReader(null);
      for await (const piece of body) reader.push(piece);
      reader.end();
      for (const s of reader.sections) {
        const change: WorkspaceChange = { path: '', status: s.status };
        if (s.binary) change.binary = true;
        else {
          change.added = s.added;
          change.removed = s.removed;
        }
        entries.push({ repoPath: s.repoPath, change });
      }
    } else if (entry.name.startsWith(FILE_PREFIX) && entry.type !== 'other') {
      const repoPath = entry.name.slice(FILE_PREFIX.length);
      const change: WorkspaceChange = { path: '', status: 'untracked' };
      if (entry.type === 'link') {
        change.added = 1;
        change.removed = 0;
      } else {
        const counted = await countFile(body);
        if (counted.binary) change.binary = true;
        else {
          change.added = counted.lines;
          change.removed = 0;
        }
      }
      entries.push({ repoPath, change });
    }
    return undefined;
  });
  const byPath: SnapshotView['byPath'] = new Map();
  for (const { repoPath, change } of entries) {
    const local = localOf(meta.subdir, repoPath);
    if (local === null) continue;
    change.path = local;
    byPath.set(local, { change, repoPath });
  }
  const changes = [...byPath.values()]
    .map((e) => e.change)
    .sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
  return { ...meta, changes, byPath };
}

/** The parsed changes of the session's latest snapshot, from the cache when it holds them. */
async function viewOf(
  ctx: StoreContext,
  sessionId: string,
): Promise<{ record: SnapshotRecord; view: SnapshotView } | null> {
  const record = latestSnapshot(ctx, sessionId);
  if (!record) return null;
  const cached = ctx.snapshotViews.get(record.snapshotId);
  if (cached) {
    // Most recently used last.
    ctx.snapshotViews.delete(record.snapshotId);
    ctx.snapshotViews.set(record.snapshotId, cached);
    return { record, view: await cached };
  }
  const parsing = withArchive(ctx, sessionId, (_record, bytes) => parseSnapshot(bytes));
  const view = parsing.then((r) => {
    if (!r) throw new SnapshotReadError('snapshot-replaced');
    return r;
  });
  const entry = view.then((r) => r.value);
  entry.catch(() => ctx.snapshotViews.delete(record.snapshotId));
  ctx.snapshotViews.set(record.snapshotId, entry);
  while (ctx.snapshotViews.size > CACHED_SNAPSHOTS) {
    const oldest = ctx.snapshotViews.keys().next().value as string;
    ctx.snapshotViews.delete(oldest);
  }
  const done = await view;
  return { record: done.record, view: done.value };
}

/** The changes of the latest snapshot of a session, or null when it has none. */
export async function snapshotChanges(
  ctx: StoreContext,
  sessionId: string,
): Promise<SnapshotChanges | null> {
  const found = await viewOf(ctx, sessionId);
  if (!found) return null;
  return {
    sessionId,
    snapshotAt: found.record.createdAt,
    baseCommit: found.record.baseCommit,
    origin: found.view.origin,
    byteSize: found.record.byteSize,
    changes: found.view.changes,
  };
}

/** The diff of one changed path of the latest snapshot of a session. */
export async function snapshotDiff(
  ctx: StoreContext,
  sessionId: string,
  localPath: string,
): Promise<SnapshotDiffResult> {
  const found = await viewOf(ctx, sessionId);
  if (!found) return { ok: false, error: 'no-snapshot' };
  const hit = found.view.byPath.get(localPath);
  if (!hit) return { ok: false, error: 'not-found' };
  const { change, repoPath } = hit;
  const stamp = { snapshotAt: found.record.createdAt, baseCommit: found.record.baseCommit };
  const base = { sessionId, path: localPath, status: change.status };
  const scanned = await withArchive(ctx, sessionId, async (record, bytes) => {
    if (record.snapshotId !== found.record.snapshotId)
      throw new SnapshotReadError('snapshot-replaced');
    let result: SnapshotDiff | null = null;
    await scanTar(bytes, async (entry, body) => {
      if (change.status !== 'untracked' && entry.name === DIFF_NAME) {
        result = { ...(await trackedDiff(body, repoPath, base)), ...stamp };
        return true;
      }
      if (change.status === 'untracked' && entry.name === FILE_PREFIX + repoPath) {
        result = { ...(await untrackedDiff(entry, body, change, base)), ...stamp };
        return true;
      }
      return undefined;
    });
    return result as SnapshotDiff | null;
  });
  if (!scanned?.value) throw new SnapshotReadError('snapshot-corrupt');
  return { ok: true, diff: scanned.value };
}

type DiffBase = { sessionId: string; path: string; status: WorkspaceChange['status'] };

async function trackedDiff(body: AsyncIterable<Buffer>, repoPath: string, base: DiffBase) {
  const capture: Capture = {
    repoPath,
    section: null,
    kept: [],
    keptBytes: 0,
    size: 0,
    truncated: false,
    patches: [],
    done: false,
  };
  const reader = new DiffReader(capture);
  for await (const piece of body) {
    reader.push(piece);
    if (capture.done) break;
  }
  if (!capture.done) reader.end();
  if (capture.section?.binary) {
    const [forward, reverse] = capture.patches;
    const newSize = base.status !== 'deleted' && forward ? patchResultSize(forward) : undefined;
    const oldSize = base.status !== 'added' && reverse ? patchResultSize(reverse) : undefined;
    return {
      kind: 'binary' as const,
      ...base,
      ...(oldSize !== undefined ? { oldSize } : {}),
      ...(newSize !== undefined ? { newSize } : {}),
    };
  }
  return {
    kind: 'text' as const,
    ...base,
    diff: cutAtLine(Buffer.concat(capture.kept), capture.truncated),
    truncated: capture.truncated,
    size: capture.size,
  };
}

async function untrackedDiff(
  entry: TarEntry,
  body: AsyncIterable<Buffer>,
  change: WorkspaceChange,
  base: DiffBase,
) {
  if (change.binary) return { kind: 'binary' as const, ...base, newSize: entry.size };
  const content = entry.type === 'link' ? oneChunk(Buffer.from(entry.linkname)) : body;
  const added = await addedDiff(content, change.added ?? 0);
  return { kind: 'text' as const, ...base, ...added };
}

async function* oneChunk(b: Buffer): AsyncGenerator<Buffer> {
  yield b;
}
