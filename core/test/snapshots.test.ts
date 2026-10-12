import { createHash } from 'node:crypto';
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION, SNAPSHOT_CHUNK_BYTES, SNAPSHOT_MAX_BYTES } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { type SnapshotChunk, SnapshotError, Store } from '../src/store/index.ts';

const dirs: string[] = [];
afterEach(() => {
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

function newStore(): { store: Store; dir: string } {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-snapshots-'));
  dirs.push(dir);
  return { store: new Store(dir), dir };
}

const COMMIT = 'a'.repeat(40);
const TREE = 'b'.repeat(40);

/** A syntactically valid archive of about `size` bytes: gzip magic, then bytes the store does not read. */
function archive(size: number, fill = 7): Buffer {
  const bytes = Buffer.alloc(size, fill);
  bytes[0] = 0x1f;
  bytes[1] = 0x8b;
  return bytes;
}

/** The chunks of `bytes` as an agent sends them. */
function chunks(
  sessionId: string,
  snapshotId: string,
  bytes: Buffer,
  piece = SNAPSHOT_CHUNK_BYTES,
): SnapshotChunk[] {
  const sha256 = createHash('sha256').update(bytes).digest('hex');
  const out: SnapshotChunk[] = [];
  for (let offset = 0, seq = 0; offset < bytes.length; offset += piece, seq++) {
    out.push({
      sessionId,
      snapshotId,
      seq,
      last: offset + piece >= bytes.length,
      data: bytes.subarray(offset, offset + piece).toString('base64'),
      createdAt: 1_700_000_000_000,
      baseCommit: COMMIT,
      originUrl: 'git@example.test:o/r.git',
      treeHash: TREE,
      byteSize: bytes.length,
      sha256,
    });
  }
  return out;
}

function upload(store: Store, all: SnapshotChunk[]) {
  let record = null;
  for (const chunk of all) record = store.putSnapshotChunk(chunk);
  return record;
}

const files = (dir: string, session: string): string[] =>
  readdirSync(path.join(dir, 'snapshots', session)).sort();

describe('snapshot storage', () => {
  it('stores a snapshot in ordered chunks and keeps only the latest per session', async () => {
    const { store, dir } = newStore();
    const first = archive(2.5 * SNAPSHOT_CHUNK_BYTES, 1);
    const record = upload(store, chunks('s1', 'one', first));
    expect(record).toMatchObject({
      sessionId: 's1',
      snapshotId: 'one',
      byteSize: first.length,
      baseCommit: COMMIT,
      treeHash: TREE,
      originUrl: 'git@example.test:o/r.git',
    });
    expect(store.latestSnapshot('s1')).toEqual(record);
    expect(readFileSync(path.join(dir, 'snapshots/s1/one.tar.gz')).equals(first)).toBe(true);

    const second = archive(1000, 2);
    upload(store, chunks('s1', 'two', second));
    expect(store.latestSnapshot('s1')?.snapshotId).toBe('two');
    expect(files(dir, 's1')).toEqual(['two.tar.gz']);

    upload(store, chunks('s2', 'other', archive(10)));
    expect(store.latestSnapshot('s2')?.snapshotId).toBe('other');
    expect(files(dir, 's1')).toEqual(['two.tar.gz']);
    expect(store.latestSnapshot('absent')).toBeNull();
    expect(store.latestSnapshot('../escape')).toBeNull();

    const read: Buffer[] = [];
    for await (const piece of store.readSnapshot('s1', 'two')) {
      expect(piece.length).toBeLessThanOrEqual(SNAPSHOT_CHUNK_BYTES);
      read.push(piece);
    }
    expect(Buffer.concat(read).equals(second)).toBe(true);
    await expect(
      (async () => {
        for await (const _ of store.readSnapshot('s1', 'one')) {
          // the first snapshot was replaced
        }
      })(),
    ).rejects.toThrow('snapshot-replaced');
  });

  it('accepts a snapshot at exactly the size limit and refuses one byte more before storing', () => {
    const { store, dir } = newStore();
    const atLimit = archive(SNAPSHOT_MAX_BYTES, 3);
    expect(upload(store, chunks('s1', 'max', atLimit))?.byteSize).toBe(SNAPSHOT_MAX_BYTES);

    const over = chunks('s1', 'over', archive(SNAPSHOT_MAX_BYTES + 1, 4), SNAPSHOT_CHUNK_BYTES);
    const first = over[0];
    if (!first) throw new Error('no chunk');
    expect(() => store.putSnapshotChunk(first)).toThrow(SnapshotError);
    expect(store.latestSnapshot('s1')?.snapshotId).toBe('max');
    expect(files(dir, 's1')).toEqual(['max.tar.gz']);
  });

  it('refuses what exceeds its own promise, byte by byte', () => {
    const { store, dir } = newStore();
    const bytes = archive(3000);
    const [head] = chunks('s1', 'x', bytes);
    if (!head) throw new Error('no chunk');
    // Promises 1000 bytes, then sends 3000 in one chunk.
    expect(() => store.putSnapshotChunk({ ...head, byteSize: 1000, last: true })).toThrow(
      SnapshotError,
    );
    // Promises 3000 and sends 2999.
    const short = { ...head, data: bytes.subarray(0, 2999).toString('base64') };
    expect(() => store.putSnapshotChunk({ ...short, last: true })).toThrow(/size-mismatch/);
    expect(store.latestSnapshot('s1')).toBeNull();
    expect(existsSync(path.join(dir, 'snapshots/s1')) ? files(dir, 's1') : []).toEqual([]);
  });

  it('refuses a chunk over 1 MiB, bad base64, a wrong checksum and a non-gzip archive', () => {
    const { store } = newStore();
    const good = archive(5000);
    const [head] = chunks('s1', 'x', good);
    if (!head) throw new Error('no chunk');
    const oversized = Buffer.alloc(SNAPSHOT_CHUNK_BYTES + 3, 1);
    expect(() =>
      store.putSnapshotChunk({
        ...head,
        byteSize: oversized.length,
        data: oversized.toString('base64'),
      }),
    ).toThrow(/invalid-chunk/);
    for (const data of ['', '!!!!', 'abc', 'ab==cd', 'AAAA\nAAAA']) {
      expect(() => store.putSnapshotChunk({ ...head, data })).toThrow(/invalid-chunk/);
    }
    expect(() => store.putSnapshotChunk({ ...head, sha256: '0'.repeat(64) })).toThrow(
      /checksum-mismatch/,
    );
    const plain = Buffer.alloc(100, 9);
    expect(() => upload(store, chunks('s1', 'plain', plain))).toThrow(/not-gzip/);
    expect(store.latestSnapshot('s1')).toBeNull();
  });

  it('refuses invalid identifiers and metadata before touching the disk', () => {
    const { store, dir } = newStore();
    const [head] = chunks('s1', 'x', archive(100));
    if (!head) throw new Error('no chunk');
    for (const bad of [
      { sessionId: '../s1' },
      { sessionId: 'a/b' },
      { snapshotId: '../x' },
      { snapshotId: '' },
      { snapshotId: 'x'.repeat(65) },
      { baseCommit: 'HEAD' },
      { treeHash: 'zz' },
      { sha256: 'abc' },
      { originUrl: 'a\nb' },
      { createdAt: 0 },
      { byteSize: 0 },
    ]) {
      expect(() => store.putSnapshotChunk({ ...head, ...bad })).toThrow(SnapshotError);
    }
    expect(
      existsSync(path.join(dir, 'snapshots')) ? readdirSync(path.join(dir, 'snapshots')) : [],
    ).toEqual([]);
  });

  it('refuses chunks out of order, repeated or of another snapshot, and keeps the previous one', () => {
    const { store, dir } = newStore();
    upload(store, chunks('s1', 'old', archive(100, 5)));
    const all = chunks('s1', 'new', archive(3 * SNAPSHOT_CHUNK_BYTES, 6));
    const [c0, c1, c2] = all;
    if (!c0 || !c1 || !c2) throw new Error('chunks');
    store.putSnapshotChunk(c0);
    expect(() => store.putSnapshotChunk(c2)).toThrow(/out-of-order/);
    // The upload is gone after a violation; its remaining chunks have nothing to attach to.
    expect(() => store.putSnapshotChunk(c1)).toThrow(/out-of-order/);
    expect(store.latestSnapshot('s1')?.snapshotId).toBe('old');

    store.putSnapshotChunk(c0);
    store.putSnapshotChunk(c1);
    expect(() => store.putSnapshotChunk(c1)).toThrow(/out-of-order/);
    store.putSnapshotChunk(c0);
    expect(() => store.putSnapshotChunk({ ...c1, snapshotId: 'different' })).toThrow(
      /out-of-order/,
    );
    store.putSnapshotChunk(c0);
    expect(() => store.putSnapshotChunk({ ...c1, baseCommit: 'c'.repeat(40) })).toThrow(
      /out-of-order/,
    );
    expect(store.latestSnapshot('s1')?.snapshotId).toBe('old');
    expect(files(dir, 's1')).toEqual(['old.tar.gz']);
  });

  it('restarts an upload at seq 0 and discards the unfinished one', () => {
    const { store, dir } = newStore();
    const [c0] = chunks('s1', 'half', archive(3 * SNAPSHOT_CHUNK_BYTES));
    if (!c0) throw new Error('chunks');
    store.putSnapshotChunk(c0);
    expect(files(dir, 's1')).toEqual(['.half.part']);
    upload(store, chunks('s1', 'whole', archive(50)));
    expect(files(dir, 's1')).toEqual(['whole.tar.gz']);
  });

  it('removes unfinished uploads and unnamed files at startup and forgets a missing file', () => {
    const { store, dir } = newStore();
    upload(store, chunks('s1', 'kept', archive(100)));
    upload(store, chunks('s2', 'lost', archive(100)));
    const [c0] = chunks('s3', 'unfinished', archive(3 * SNAPSHOT_CHUNK_BYTES));
    if (!c0) throw new Error('chunks');
    store.putSnapshotChunk(c0);
    writeFileSync(path.join(dir, 'snapshots/s1/orphan.tar.gz'), 'x');
    rmSync(path.join(dir, 'snapshots/s2/lost.tar.gz'));
    mkdirSync(path.join(dir, 'snapshots/not a session id'), { recursive: true });

    const reopened = new Store(dir);
    expect(files(dir, 's1')).toEqual(['kept.tar.gz']);
    expect(reopened.latestSnapshot('s1')?.snapshotId).toBe('kept');
    expect(reopened.latestSnapshot('s2')).toBeNull();
    expect(existsSync(path.join(dir, 'snapshots/s3'))).toBe(false);
    expect(existsSync(path.join(dir, 'snapshots/not a session id'))).toBe(true);
  });
});

describe('snapshot upload over the agent socket', () => {
  async function harness() {
    const { store, dir } = newStore();
    const server = createServer();
    attachIngest(server, store, createControl(store));
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
    const port = (server.address() as AddressInfo).port;
    const sockets: WebSocket[] = [];
    const connect = async (id: string) => {
      const minted = store.mintDevice(id, id);
      const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
        headers: { authorization: `Bearer ${minted.token}` },
      });
      sockets.push(ws);
      const inbox: Record<string, unknown>[] = [];
      ws.on('message', (raw) => inbox.push(JSON.parse(String(raw))));
      await new Promise<void>((resolve, reject) => {
        ws.once('open', () => resolve());
        ws.once('error', reject);
      });
      ws.send(
        JSON.stringify({
          v: PROTOCOL_VERSION,
          type: 'device.hello',
          deviceId: id,
          name: id,
          version: 't',
        }),
      );
      return { ws, inbox };
    };
    const until = async (check: () => boolean) => {
      const deadline = Date.now() + 5000;
      while (!check()) {
        if (Date.now() > deadline) throw new Error('timed out');
        await new Promise((r) => setTimeout(r, 10));
      }
    };
    const close = async () => {
      for (const ws of sockets) ws.terminate();
      await new Promise<void>((resolve) => server.close(() => resolve()));
    };
    const getErrors = (inbox: Record<string, unknown>[]) => inbox.filter((m) => m.type === 'error');
    return { store, dir, connect, until, close, getErrors };
  }

  const wire = (c: SnapshotChunk) => ({ v: PROTOCOL_VERSION, type: 'workspace.snapshot', ...c });

  it('stores the snapshot of the owner and refuses everyone else', async () => {
    const h = await harness();
    try {
      h.store.append({ sessionId: 's1', projectDir: 'p', filePath: '/x', deviceId: 'owner' }, [
        '{}',
      ]);
      const owner = await h.connect('owner');
      const other = await h.connect('other');
      const bytes = archive(2 * SNAPSHOT_CHUNK_BYTES + 5);

      for (const chunk of chunks('s1', 'mine', bytes)) other.ws.send(JSON.stringify(wire(chunk)));
      await h.until(() => h.getErrors(other.inbox).length > 0);
      expect(h.store.latestSnapshot('s1')).toBeNull();

      for (const chunk of chunks('s1', 'mine', bytes)) owner.ws.send(JSON.stringify(wire(chunk)));
      await h.until(() => h.store.latestSnapshot('s1')?.snapshotId === 'mine');
      expect(readFileSync(path.join(h.dir, 'snapshots/s1/mine.tar.gz')).equals(bytes)).toBe(true);

      // A session the core does not know has no snapshots either.
      for (const chunk of chunks('unknown', 'x', archive(10)))
        owner.ws.send(JSON.stringify(wire(chunk)));
      await h.until(() => h.getErrors(owner.inbox).length > 0);
      expect(h.getErrors(owner.inbox)[0]).toMatchObject({
        type: 'error',
        code: 'snapshot-rejected',
      });
      expect(h.store.latestSnapshot('unknown')).toBeNull();
    } finally {
      await h.close();
    }
  });

  it('refuses a session ID that is not valid before looking at the payload', async () => {
    const h = await harness();
    try {
      const owner = await h.connect('owner');
      const [chunk] = chunks('s1', 'x', archive(10));
      if (!chunk) throw new Error('chunks');
      owner.ws.send(JSON.stringify(wire({ ...chunk, sessionId: '../etc' })));
      await h.until(() => h.getErrors(owner.inbox).length > 0);
      expect(h.getErrors(owner.inbox)[0]).toMatchObject({ code: 'invalid-session-id' });
    } finally {
      await h.close();
    }
  });

  it('refuses a chunk whose data is longer than a chunk can be', async () => {
    const h = await harness();
    try {
      h.store.append({ sessionId: 's1', projectDir: 'p', filePath: '/x', deviceId: 'owner' }, [
        '{}',
      ]);
      const owner = await h.connect('owner');
      const [chunk] = chunks('s1', 'x', archive(10));
      if (!chunk) throw new Error('chunks');
      owner.ws.send(JSON.stringify(wire({ ...chunk, data: 'A'.repeat(SNAPSHOT_CHUNK_BYTES * 2) })));
      await h.until(() => h.getErrors(owner.inbox).length > 0);
      expect(h.getErrors(owner.inbox)[0]).toMatchObject({ code: 'invalid-message' });
    } finally {
      await h.close();
    }
  });
});
