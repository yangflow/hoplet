import { spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, readdirSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { gunzipSync } from 'node:zlib';
import { afterEach, expect, it } from 'vitest';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
afterEach(() => {
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});
const input = { sessionId: 'recovery', projectDir: 'p', filePath: '/synthetic', deviceId: 'd' };
const line = (i: number) => JSON.stringify({ n: i, text: '原始\r bytes' });
const raw = (count: number) =>
  Buffer.from(Array.from({ length: count }, (_, i) => `${line(i)}\n`).join(''));
function bytes(dir: string): Buffer {
  const sessionDir = path.join(dir, 'sessions', 'recovery');
  const seqs = [
    ...new Set(
      readdirSync(sessionDir).flatMap((f) => /^c-(\d+)\.ndjson(?:\.gz)?$/.exec(f)?.[1] ?? []),
    ),
  ]
    .map(Number)
    .sort((a, b) => a - b);
  return Buffer.concat(
    seqs.map((seq) => {
      const file = path.join(sessionDir, `c-${seq}.ndjson`);
      return existsSync(file) ? readFileSync(file) : gunzipSync(readFileSync(`${file}.gz`));
    }),
  );
}
const scenarios = [
  ['partial-append', 2, 3, false],
  ['after-append', 2, 3, false],
  ['before-index', 499, 502, false],
  ['after-index', 499, 502, false],
  ['append-synced', 500, 1, false],
  ['after-commit', 499, 502, true],
  ['after-append', 0, 1, false],
  ['second-append', 499, 502, false],
  ['partial-gzip', 499, 502, true],
  ['gzip-written', 499, 502, true],
  ['gzip-renamed', 499, 502, true],
  ['plain-unlinked', 499, 502, true],
] as const;
for (const [point, seed, count, committed] of scenarios) {
  it(`recovers byte-exact storage after SIGKILL at ${point}`, async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-recovery-'));
    dirs.push(dir);
    const child = spawnSync(
      process.execPath,
      ['core/test/fixtures/interrupted-append.ts', dir, point, 'kill', String(seed), String(count)],
      { encoding: 'utf8' },
    );
    expect(child.signal, child.stderr).toBe('SIGKILL');
    const store = new Store(dir);
    const accepted = committed ? seed + count : seed;
    expect(store.listSessions()[0]?.byteSize).toBe(raw(accepted).length);
    expect(store.getSession('recovery')?.lineCount).toBe(accepted);
    expect(bytes(dir).equals(raw(accepted))).toBe(true);
    store.append(
      input,
      Array.from({ length: seed + count - accepted }, (_, i) => line(accepted + i)),
    );
    expect(bytes(dir).equals(raw(seed + count))).toBe(true);
    expect((await store.readLines('recovery', 0, 2000))?.lines).toEqual(
      Array.from({ length: seed + count }, (_, i) => JSON.parse(line(i))),
    );
    expect((await store.search('recovery', 'bytes', 0, 2000))?.total).toBe(seed + count);
  });
}
for (const point of ['partial-append', 'after-append', 'before-index']) {
  it(`rolls back a function error at ${point} before same-process retry`, () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-recovery-'));
    dirs.push(dir);
    const child = spawnSync(
      process.execPath,
      ['core/test/fixtures/interrupted-append.ts', dir, point, 'throw', '2', '3'],
      { encoding: 'utf8' },
    );
    expect(child.status, child.stderr).toBe(0);
    expect(existsSync(path.join(dir, 'retried'))).toBe(true);
    expect(new Store(dir).getSession('recovery')?.byteSize).toBe(raw(5).length);
    expect(bytes(dir).equals(raw(5))).toBe(true);
  });
}

for (const recoveryPoint of ['undo-truncated', 'undo-removed']) {
  it(`survives a second SIGKILL at ${recoveryPoint}`, async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-recovery-'));
    dirs.push(dir);
    for (const [point, seed, count] of [
      ['before-index', '499', '502'],
      [recoveryPoint, '0', '0'],
    ] as const) {
      const child = spawnSync(
        process.execPath,
        ['core/test/fixtures/interrupted-append.ts', dir, point, 'kill', seed, count],
        { encoding: 'utf8' },
      );
      expect(child.signal, child.stderr).toBe('SIGKILL');
    }
    const store = new Store(dir);
    expect(store.getSession('recovery')?.byteSize).toBe(raw(499).length);
    expect(bytes(dir).equals(raw(499))).toBe(true);
    expect((await new Store(dir).readLines('recovery', -1, 1))?.lines).toEqual([
      JSON.parse(line(498)),
    ]);
  });
}

it('completes short writes before committing the exact cursor', () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-recovery-'));
  dirs.push(dir);
  const child = spawnSync(
    process.execPath,
    ['core/test/fixtures/interrupted-append.ts', dir, 'short-write', 'throw', '2', '3'],
    { encoding: 'utf8' },
  );
  expect(child.status, child.stderr).toBe(0);
  expect(new Store(dir).getSession('recovery')?.byteSize).toBe(raw(5).length);
  expect(bytes(dir).equals(raw(5))).toBe(true);
});

for (const point of [
  'partial-gzip',
  'gzip-written',
  'gzip-renamed',
  'plain-unlinked',
  'after-commit',
]) {
  it(`keeps committed bytes after a function error at ${point}`, () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-recovery-'));
    dirs.push(dir);
    const child = spawnSync(
      process.execPath,
      ['core/test/fixtures/interrupted-append.ts', dir, point, 'throw', '499', '502'],
      { encoding: 'utf8' },
    );
    expect(child.status, child.stderr).toBe(0);
    const visible = JSON.parse(readFileSync(path.join(dir, 'visible.json'), 'utf8'));
    expect(visible.meta.byteSize).toBe(raw(1001).length);
    expect(visible.page.lines).toEqual(Array.from({ length: 1001 }, (_, i) => JSON.parse(line(i))));
    expect(bytes(dir).equals(raw(1001))).toBe(true);
    expect(new Store(dir).getSession('recovery')?.byteSize).toBe(raw(1001).length);
    expect(bytes(dir).equals(raw(1001))).toBe(true);
  });
}

it('fails closed when undo fails, then recovers on restart', () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-recovery-'));
  dirs.push(dir);
  const child = spawnSync(
    process.execPath,
    ['core/test/fixtures/interrupted-append.ts', dir, 'undo-failed', 'throw', '2', '3'],
    { encoding: 'utf8' },
  );
  expect(child.status, child.stderr).toBe(0);
  expect(existsSync(path.join(dir, 'closed'))).toBe(true);
  const store = new Store(dir);
  expect(store.getSession('recovery')?.byteSize).toBe(raw(2).length);
  expect(bytes(dir).equals(raw(2))).toBe(true);
  store.append(input, [line(2), line(3), line(4)]);
  expect(bytes(dir).equals(raw(5))).toBe(true);
});

it('rejects a zero-progress write without advancing the cursor', () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-recovery-'));
  dirs.push(dir);
  const child = spawnSync(
    process.execPath,
    ['core/test/fixtures/interrupted-append.ts', dir, 'zero-write', 'throw', '2', '3'],
    { encoding: 'utf8' },
  );
  expect(child.status).toBe(1);
  expect(child.stderr).toContain('chunk write made no progress');
  expect(new Store(dir).getSession('recovery')?.byteSize).toBe(raw(2).length);
  expect(bytes(dir).equals(raw(2))).toBe(true);
});

it('refuses startup with incomplete undo and keeps work for a successful restart', () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-recovery-'));
  dirs.push(dir);
  const interrupted = spawnSync(
    process.execPath,
    ['core/test/fixtures/interrupted-append.ts', dir, 'before-index', 'kill', '499', '502'],
    { encoding: 'utf8' },
  );
  expect(interrupted.signal, interrupted.stderr).toBe('SIGKILL');
  const failed = spawnSync(
    process.execPath,
    ['core/test/fixtures/interrupted-append.ts', dir, 'undo-startup-failed', 'throw', '0', '0'],
    { encoding: 'utf8' },
  );
  expect(failed.status).toBe(1);
  expect(failed.stderr).toContain('injected undo failure');
  expect(new Store(dir).getSession('recovery')?.byteSize).toBe(raw(499).length);
  expect(bytes(dir).equals(raw(499))).toBe(true);
});
