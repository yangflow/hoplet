import { createHash } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { docs, lines, on, type RunOptions, run as runCommand } from './support.ts';

const dirs: string[] = [];
afterEach(() => {
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

const readFile = (p: string) => ({ text: readFileSync(p, 'utf8'), mode: statSync(p).mode });
const run = (argv: string[], opts: RunOptions = {}) =>
  runCommand(argv, { ...opts, deps: { readFile, ...opts.deps } });

function event(seq: number, overrides: Record<string, unknown> = {}) {
  return {
    seq,
    at: 0,
    recordedAt: 0,
    type: 'session.stopped',
    sessionId: 's1',
    deviceId: 'd',
    actorKind: 'operator',
    actorId: 'host',
    channel: 'rest',
    ref: null,
    summary: {},
    prevHash: 'a'.repeat(64),
    hash: 'b'.repeat(64),
    ...overrides,
  };
}

/** An export of three records, hashed independently of the core. */
function exportText(): string {
  const records: Record<string, unknown>[] = [];
  let prev = '0'.repeat(64);
  for (let seq = 1; seq <= 3; seq++) {
    const body = `{"actor_id":"","actor_kind":"core","at":${seq},"channel":"core","device_id":null,"recorded_at":${seq},"ref":null,"seq":${seq},"session_id":null,"summary":{},"type":"audit.started"}`;
    const hash = createHash('sha256').update(prev).update(body).digest('hex');
    records.push({
      seq,
      at: seq,
      recordedAt: seq,
      type: 'audit.started',
      sessionId: null,
      deviceId: null,
      actorKind: 'core',
      actorId: '',
      channel: 'core',
      ref: null,
      summary: {},
      prevHash: prev,
      hash,
    });
    prev = hash;
  }
  return `${records.map((l) => JSON.stringify(l)).join('\n')}\n`;
}

function file(text: string): string {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-cli-audit-'));
  dirs.push(dir);
  const p = path.join(dir, 'audit.jsonl');
  writeFileSync(p, text);
  return p;
}

describe('hoplet audit', () => {
  it('lists records as lines, with the filters in the query', async () => {
    const r = await run(
      ['audit', 'list', '--session', 's1', '--type', 'session.', '--limit', '5', '--asc'],
      { handlers: [on('GET', '/api/audit/events', { json: { events: [event(2)], next: null } })] },
    );
    expect(r.code).toBe(0);
    expect(lines(r.out)).toEqual([
      '2\t1970-01-01T00:00:00.000Z\tsession.stopped\toperator:host\ts1\t{}',
    ]);
    const query = r.calls.find((c) => c.path.startsWith('/api/audit/events?'))?.path ?? '';
    expect(query).toContain('session=s1');
    expect(query).toContain('type=session.');
    expect(query).toContain('limit=5');
    expect(query).toContain('order=asc');
  });

  it('lists records as one document with --json', async () => {
    const r = await run(['audit', 'list', '--json'], {
      handlers: [on('GET', '/api/audit/events', { json: { events: [event(2)], next: null } })],
    });
    expect(docs(r.out)).toMatchObject([{ events: [{ seq: 2 }], next: null }]);
  });

  it('prints the head, or empty', async () => {
    const head = (events: unknown[]) => [
      on('GET', '/api/audit/events', { json: { events, next: null } }),
    ];
    expect(lines((await run(['audit', 'head'], { handlers: head([]) })).out)).toEqual(['empty']);
    const one = await run(['audit', 'head'], {
      handlers: head([event(4, { hash: 'c'.repeat(64) })]),
    });
    expect(lines(one.out)).toEqual([`4:${'c'.repeat(64)}`]);
  });

  it('exports the text the core streams', async () => {
    const r = await run(['audit', 'export', '--format', 'csv'], {
      handlers: [on('GET', '/api/audit/export', { text: 'a,b\r\n1,2\r\n' })],
    });
    expect(r.code).toBe(0);
    expect(r.out).toBe('a,b\r\n1,2\r\n');
    expect(r.calls.find((c) => c.path.startsWith('/api/audit/export?'))?.path).toContain(
      'format=csv',
    );
    expect((await run(['audit', 'export', '--format', 'xml'])).code).toBe(1);
  });

  it('verifies on the server and exits 3 on a break', async () => {
    const verdict = (body: unknown) => ({
      handlers: [on('GET', '/api/audit/verify', { json: body })],
    });
    const good = await run(
      ['audit', 'verify'],
      verdict({ ok: true, checked: 2, head: null, firstBreak: null }),
    );
    expect(good.code).toBe(0);
    const bad = await run(
      ['audit', 'verify'],
      verdict({
        ok: false,
        checked: 1,
        head: null,
        firstBreak: { seq: 2, reason: 'hash-mismatch', message: 'record 2 does not match' },
      }),
    );
    expect(bad.code).toBe(3);
    expect(bad.err).toContain('audit chain broken at seq 2');
    const json = await run(['audit', 'verify', '--json'], {
      handlers: verdict({
        ok: false,
        checked: 1,
        head: null,
        firstBreak: { seq: 2, reason: 'hash-mismatch', message: 'm' },
      }).handlers,
    });
    expect(json.code).toBe(3);
    expect(docs(json.err)).toMatchObject([{ code: 'audit-broken' }]);
  });

  it('verifies a file without a request or a token, and finds a change', async () => {
    const text = exportText();
    const ok = await run(['audit', 'verify', '--file', file(text)], {
      env: { HOPLET_TOKEN: undefined },
    });
    expect(ok.code).toBe(0);
    expect(ok.calls).toEqual([]);
    expect(docs(ok.out)).toMatchObject([{ ok: true, checked: 3 }]);

    const tampered = await run([
      'audit',
      'verify',
      '--file',
      file(text.replace('"at":2', '"at":9')),
    ]);
    expect(tampered.code).toBe(3);
    expect(docs(tampered.out)).toMatchObject([{ firstBreak: { seq: 2, reason: 'hash-mismatch' } }]);

    const gap = text
      .split('\n')
      .filter((_, i) => i !== 1)
      .join('\n');
    const removed = await run(['audit', 'verify', '--file', file(gap)]);
    expect(docs(removed.out)).toMatchObject([{ firstBreak: { reason: 'gap' } }]);
  });

  it('checks a file against a head written down', async () => {
    const text = exportText();
    const lastHash = JSON.parse(text.trim().split('\n')[2] ?? '{}').hash as string;
    const match = await run(['audit', 'verify', '--file', file(text), '--head', `3:${lastHash}`]);
    expect(match.code).toBe(0);
    const wrong = await run([
      'audit',
      'verify',
      '--file',
      file(text),
      '--head',
      `3:${'a'.repeat(64)}`,
    ]);
    expect(wrong.code).toBe(3);
    const beyond = await run(['audit', 'verify', '--file', file(text), '--head', `9:${lastHash}`]);
    expect(docs(beyond.out)).toMatchObject([{ firstBreak: { reason: 'head-missing' } }]);
  });

  it('refuses misplaced flags, a bad head and an unreadable file', async () => {
    expect((await run(['audit'])).code).toBe(1);
    expect((await run(['audit', 'bogus'])).code).toBe(1);
    expect((await run(['audit', 'head', '--type', 'x'])).code).toBe(1);
    expect((await run(['audit', 'verify', '--head', 'nonsense'])).code).toBe(1);
    expect((await run(['audit', 'verify', '--file', '/nonexistent/audit.jsonl'])).code).toBe(1);
    expect((await run(['devices', '--type', 'x'])).code).toBe(1);
  });
});
