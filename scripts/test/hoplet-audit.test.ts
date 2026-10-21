import { createHash } from 'node:crypto';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { csvCell, csvLine } from '../../core/src/api/audit-export.ts';
import { parseArgv, runCli } from '../hoplet/cli.ts';

const ENV = { HOPLET_TOKEN: 't', HOPLET_URL: 'http://h' };
const dirs: string[] = [];
afterEach(() => {
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

function run(argv: string[], fetchImpl?: typeof fetch) {
  const out: string[] = [];
  const err: string[] = [];
  const code = runCli(
    argv,
    ENV,
    { stdout: (l) => out.push(l), stderr: (l) => err.push(l) },
    fetchImpl ??
      ((() => {
        throw new Error('no request expected');
      }) as unknown as typeof fetch),
  );
  return code.then((c) => ({ code: c, out, err }));
}

/** An export of three records, hashed independently of the core. */
function exportText(): string {
  const lines: Record<string, unknown>[] = [];
  let prev = '0'.repeat(64);
  for (let seq = 1; seq <= 3; seq++) {
    const body = `{"actor_id":"","actor_kind":"core","at":${seq},"channel":"core","device_id":null,"recorded_at":${seq},"ref":null,"seq":${seq},"session_id":null,"summary":{},"type":"audit.started"}`;
    const hash = createHash('sha256').update(prev).update(body).digest('hex');
    lines.push({
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
  return `${lines.map((l) => JSON.stringify(l)).join('\n')}\n`;
}

function file(text: string): string {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-cli-audit-'));
  dirs.push(dir);
  const p = path.join(dir, 'audit.jsonl');
  writeFileSync(p, text);
  return p;
}

describe('csvCell', () => {
  it('quotes a formula, a comma and a quote', () => {
    expect(csvCell('=1+1')).toBe(`"'=1+1"`);
    expect(csvCell('+1')).toBe(`"'+1"`);
    expect(csvCell('-1')).toBe(`"'-1"`);
    expect(csvCell('@a')).toBe(`"'@a"`);
    expect(csvCell('a,b')).toBe('"a,b"');
    expect(csvCell('say "x"')).toBe('"say ""x"""');
    expect(csvCell(null)).toBe('');
    expect(csvCell(7)).toBe('7');
    expect(csvCell({ a: 1 })).toBe('"{""a"":1}"');
  });

  it('ends a line with CRLF', () => {
    const line = csvLine({
      seq: 1,
      at: 1,
      recordedAt: 1,
      type: 'x',
      sessionId: null,
      deviceId: null,
      actorKind: 'core',
      actorId: '',
      channel: 'core',
      ref: null,
      summary: {},
      prevHash: 'p',
      hash: 'h',
    } as never);
    expect(line.endsWith('\r\n')).toBe(true);
  });
});

describe('parseArgv audit flags', () => {
  it('collects the flags of the audit commands', () => {
    const r = parseArgv(['audit', 'list', '--type', 'session.', '--limit', '5', '--asc'], ENV);
    expect(r).toMatchObject({
      ok: true,
      args: ['audit', 'list'],
      flags: { type: 'session.', limit: '5', asc: '1' },
    });
  });
});

describe('hoplet audit', () => {
  it('lists records as lines', async () => {
    const urls: string[] = [];
    const fetchImpl = (async (url: string) => {
      urls.push(url);
      return new Response(
        JSON.stringify({
          events: [
            {
              seq: 2,
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
            },
          ],
          next: null,
        }),
        { status: 200 },
      );
    }) as unknown as typeof fetch;
    const r = await run(['audit', 'list', '--session', 's1', '--limit', '5'], fetchImpl);
    expect(r.code).toBe(0);
    expect(r.out).toEqual(['2\t1970-01-01T00:00:00.000Z\tsession.stopped\toperator:host\ts1\t{}']);
    expect(urls[0]).toContain('/api/audit/events?');
    expect(urls[0]).toContain('session=s1');
    expect(urls[0]).toContain('limit=5');
  });

  it('prints the head, or empty', async () => {
    const head = (events: unknown[]) =>
      (async () =>
        new Response(JSON.stringify({ events, next: null }), {
          status: 200,
        })) as unknown as typeof fetch;
    expect((await run(['audit', 'head'], head([]))).out).toEqual(['empty']);
    const one = await run(
      ['audit', 'head'],
      head([
        {
          seq: 4,
          at: 0,
          recordedAt: 0,
          type: 'x',
          sessionId: null,
          deviceId: null,
          actorKind: 'core',
          actorId: '',
          channel: 'core',
          ref: null,
          summary: {},
          prevHash: 'a'.repeat(64),
          hash: 'c'.repeat(64),
        },
      ]),
    );
    expect(one.out).toEqual([`4:${'c'.repeat(64)}`]);
  });

  it('exports the text the core streams', async () => {
    const fetchImpl = (async (url: string) => {
      expect(url).toContain('/api/audit/export?');
      expect(url).toContain('format=csv');
      return new Response('a,b\r\n1,2\r\n', { status: 200 });
    }) as unknown as typeof fetch;
    const r = await run(['audit', 'export', '--format', 'csv'], fetchImpl);
    expect(r.code).toBe(0);
    expect(r.out).toEqual(['a,b\r\n1,2']);
  });

  it('verifies on the server and exits 3 on a break', async () => {
    const verdict = (body: unknown) =>
      (async () => new Response(JSON.stringify(body), { status: 200 })) as unknown as typeof fetch;
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
    expect(bad.err[0]).toContain('seq 2');
  });

  it('verifies a file without a request, and finds a change', async () => {
    const text = exportText();
    const ok = await run(['audit', 'verify', '--file', file(text)]);
    expect(ok.code).toBe(0);
    expect(JSON.parse(ok.out[0] ?? '')).toMatchObject({ ok: true, checked: 3 });

    const tampered = await run([
      'audit',
      'verify',
      '--file',
      file(text.replace('"at":2', '"at":9')),
    ]);
    expect(tampered.code).toBe(3);
    expect(JSON.parse(tampered.out[0] ?? '')).toMatchObject({
      firstBreak: { seq: 2, reason: 'hash-mismatch' },
    });

    const gap = text
      .split('\n')
      .filter((_, i) => i !== 1)
      .join('\n');
    const removed = await run(['audit', 'verify', '--file', file(gap)]);
    expect(JSON.parse(removed.out[0] ?? '')).toMatchObject({ firstBreak: { reason: 'gap' } });
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
    expect(JSON.parse(beyond.out[0] ?? '')).toMatchObject({
      firstBreak: { reason: 'head-missing' },
    });
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
