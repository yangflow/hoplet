// Reading the changes and diffs of a stored snapshot (docs/spec.md#workspace), against an archive the agent's
// own snapshot code writes (agent/internal/workspace/snapshot_fixture_test.go).
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { SNAPSHOT_CHUNK_BYTES } from '@hoplet/shared';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { createApi } from '../src/api/index.ts';
import { createControl } from '../src/control/index.ts';
import { type SnapshotChunk, Store } from '../src/store/index.ts';
import { diffGitPath, MAX_DIFF_BYTES } from '../src/store/snapshot-read.ts';

const root = path.resolve(import.meta.dirname, '../..');
const dirs: string[] = [];
const servers: Server[] = [];
let archive: Buffer;
let meta: { baseCommit: string; treeHash: string; createdAt: number; originUrl: string };

beforeAll(() => {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-snapshot-fixture-'));
  dirs.push(dir);
  const file = path.join(dir, 'snapshot.tar.gz');
  execFileSync(
    'go',
    ['test', '-count=1', '-run', '^TestSnapshotFixtureForTheCore$', './internal/workspace'],
    { cwd: path.join(root, 'agent'), env: { ...process.env, HOPLET_SNAPSHOT_FIXTURE: file } },
  );
  archive = readFileSync(file);
  meta = JSON.parse(readFileSync(`${file}.json`, 'utf8')) as typeof meta;
}, 180_000);

afterAll(async () => {
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

function newStore(): Store {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-snapshot-read-'));
  dirs.push(dir);
  const store = new Store(dir);
  store.append(
    { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'dev-a' },
    ['{}'],
  );
  return store;
}

function upload(store: Store, bytes: Buffer, snapshotId = 'snap1'): void {
  const sha256 = createHash('sha256').update(bytes).digest('hex');
  for (let offset = 0, seq = 0; offset < bytes.length; offset += SNAPSHOT_CHUNK_BYTES, seq++) {
    const chunk: SnapshotChunk = {
      sessionId: 's1',
      snapshotId,
      seq,
      last: offset + SNAPSHOT_CHUNK_BYTES >= bytes.length,
      data: bytes.subarray(offset, offset + SNAPSHOT_CHUNK_BYTES).toString('base64'),
      createdAt: meta.createdAt,
      baseCommit: meta.baseCommit,
      originUrl: meta.originUrl,
      treeHash: meta.treeHash,
      byteSize: bytes.length,
      sha256,
    };
    store.putSnapshotChunk(chunk);
  }
}

const deep = `${'d'.repeat(60)}/${'e'.repeat(60)}/fresh.txt`;

describe('snapshot changes', () => {
  it('lists the changes inside the working directory, relative to it, with their counts', async () => {
    const store = newStore();
    expect(await store.snapshotChanges('s1')).toBeNull();
    upload(store, archive);
    const result = await store.snapshotChanges('s1');
    expect(result).toMatchObject({
      sessionId: 's1',
      snapshotAt: meta.createdAt,
      baseCommit: meta.baseCommit,
      origin: meta.originUrl,
      byteSize: archive.length,
    });
    expect(result?.changes).toEqual([
      { path: 'added.txt', status: 'added', added: 2, removed: 0 },
      { path: 'big.bin', status: 'modified', binary: true },
      { path: 'café x.txt', status: 'modified', added: 1, removed: 1 },
      { path: deep, status: 'untracked', added: 1, removed: 0 },
      { path: 'del.bin', status: 'deleted', binary: true },
      { path: 'edit.txt', status: 'modified', added: 1, removed: 1 },
      { path: 'empty-untracked.md', status: 'untracked', added: 0, removed: 0 },
      { path: 'gone.txt', status: 'deleted', added: 0, removed: 2 },
      { path: 'huge.txt', status: 'untracked', added: 3000, removed: 0 },
      { path: 'long.txt', status: 'modified', added: 3000, removed: 3000 },
      { path: 'new.bin', status: 'untracked', binary: true },
      { path: 'new.txt', status: 'untracked', added: 2, removed: 0 },
      { path: 'pic.bin', status: 'modified', binary: true },
      { path: 'sub/lines.txt', status: 'modified', added: 1, removed: 1 },
    ]);
  }, 30_000);
});

describe('snapshot diffs', () => {
  let store: Store;
  beforeAll(() => {
    store = newStore();
    upload(store, archive);
  });

  async function diff(p: string) {
    const found = await store.snapshotDiff('s1', p);
    if (!found.ok) throw new Error(found.error);
    expect(found.diff).toMatchObject({
      sessionId: 's1',
      path: p,
      snapshotAt: meta.createdAt,
      baseCommit: meta.baseCommit,
    });
    return found.diff;
  }

  it('returns the hunks of a tracked change without its header lines', async () => {
    expect(await diff('edit.txt')).toMatchObject({
      kind: 'text',
      status: 'modified',
      diff: '@@ -1,3 +1,3 @@\n one\n-two\n+2\n three\n',
      truncated: false,
      size: 36,
    });
    expect(await diff('gone.txt')).toMatchObject({
      status: 'deleted',
      diff: '@@ -1,2 +0,0 @@\n-a\n-b\n',
    });
    expect(await diff('added.txt')).toMatchObject({
      status: 'added',
      diff: '@@ -0,0 +1,2 @@\n+1\n+2\n',
    });
    expect(await diff('café x.txt')).toMatchObject({ diff: '@@ -1 +1 @@\n-old\n+new\n' });
    expect((await diff('sub/lines.txt')).kind).toBe('text');
  });

  it('synthesizes an untracked file as added lines', async () => {
    expect(await diff('new.txt')).toMatchObject({
      kind: 'text',
      status: 'untracked',
      diff: '@@ -0,0 +1,2 @@\n+x\n+y\n\\ No newline at end of file\n',
      truncated: false,
    });
    expect(await diff(deep)).toMatchObject({ diff: '@@ -0,0 +1 @@\n+f\n', size: 17 });
    expect(await diff('empty-untracked.md')).toMatchObject({ diff: '', size: 0 });
  });

  it('gives the sizes of a binary change: literal, delta, deleted and untracked', async () => {
    expect(await diff('pic.bin')).toMatchObject({
      kind: 'binary',
      status: 'modified',
      oldSize: 64,
      newSize: 100,
    });
    expect(await diff('big.bin')).toMatchObject({ kind: 'binary', oldSize: 40000, newSize: 40004 });
    const deleted = await diff('del.bin');
    expect(deleted).toMatchObject({ kind: 'binary', status: 'deleted', oldSize: 4 });
    expect(deleted).not.toHaveProperty('newSize');
    const untracked = await diff('new.bin');
    expect(untracked).toMatchObject({ kind: 'binary', status: 'untracked', newSize: 2 });
    expect(untracked).not.toHaveProperty('oldSize');
  });

  it('cuts a diff at 256 KiB on a line end and reports the full size', async () => {
    const line = 100;
    for (const [p, full] of [
      ['long.txt', '@@ -1,3000 +1,3000 @@\n'.length + 6000 * (line + 1)],
      ['huge.txt', '@@ -0,0 +1,3000 @@\n'.length + 3000 * (line + 1)],
    ] as const) {
      const d = await diff(p);
      if (d.kind !== 'text') throw new Error('binary');
      expect(d.truncated).toBe(true);
      expect(d.size).toBe(full);
      expect(Buffer.byteLength(d.diff)).toBeLessThanOrEqual(MAX_DIFF_BYTES);
      expect(Buffer.byteLength(d.diff)).toBeGreaterThan(MAX_DIFF_BYTES - line - 2);
      expect(d.diff.endsWith('\n')).toBe(true);
    }
  });

  it('refuses a path that did not change or lies outside the working directory', async () => {
    expect(await store.snapshotDiff('s1', 'same.txt')).toEqual({ ok: false, error: 'not-found' });
    expect(await store.snapshotDiff('s1', '../other.txt')).toEqual({
      ok: false,
      error: 'not-found',
    });
    expect(await store.snapshotDiff('s2', 'edit.txt')).toEqual({
      ok: false,
      error: 'no-snapshot',
    });
  });

  it('reads a C-quoted path of the diff header', () => {
    expect(diffGitPath('diff --git "a/caf\\303\\251\\tx" "b/caf\\303\\251\\tx"')).toBe('café\tx');
    expect(diffGitPath('diff --git a/a b c b/a b c')).toBe('a b c');
    expect(diffGitPath('diff --git x/a y/a')).toBeNull();
  });
});

describe('snapshot routes', () => {
  async function boot() {
    const store = newStore();
    const control = createControl(store, { workspaceTimeoutMs: 200 });
    const server = createServer();
    createApi(store, { port: 0, dataDir: '', token: 'host', webDir: null }, control).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const get = async (p: string) => {
      const res = await fetch(base + p, { headers: { authorization: 'Bearer host' } });
      return { status: res.status, body: (await res.json()) as Record<string, unknown> };
    };
    return { store, get };
  }

  it('answers from the stored snapshot with the owner offline, and no-snapshot without one', async () => {
    const { store, get } = await boot();
    expect(await get('/api/sessions/s1/snapshot/changes')).toEqual({
      status: 409,
      body: { code: 'no-snapshot' },
    });
    expect(await get('/api/sessions/s1/snapshot/diff?path=edit.txt')).toEqual({
      status: 409,
      body: { code: 'no-snapshot' },
    });
    upload(store, archive);
    const changes = await get('/api/sessions/s1/snapshot/changes');
    expect(changes.status).toBe(200);
    expect(changes.body).toMatchObject({ sessionId: 's1', baseCommit: meta.baseCommit });
    expect((changes.body.changes as unknown[]).length).toBe(14);
    const one = await get(
      `/api/sessions/s1/snapshot/diff?path=${encodeURIComponent('café x.txt')}`,
    );
    expect(one).toMatchObject({ status: 200, body: { kind: 'text', status: 'modified' } });
    expect(await get('/api/sessions/s1/snapshot/diff?path=same.txt')).toEqual({
      status: 404,
      body: { code: 'not-found' },
    });
    expect((await get('/api/sessions/s1/snapshot/diff')).body).toEqual({
      code: 'invalid-request',
    });
    expect(await get('/api/sessions/nope/snapshot/changes')).toEqual({
      status: 404,
      body: { code: 'session-not-found' },
    });
  }, 30_000);

  it('needs the owner online for the live diff', async () => {
    const { get } = await boot();
    expect(await get('/api/sessions/s1/workspace/diff?path=a.txt')).toEqual({
      status: 409,
      body: { code: 'owner-offline' },
    });
  });
});
