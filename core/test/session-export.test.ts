import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  type AuditEvent,
  EXPORT_DIFF_MAX_BYTES,
  type SessionExportQuery,
  SessionExportQuerySchema,
  safeParse,
  type WorkspaceChange,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { renderHeader, TurnBuilder, toolLine } from '../src/api/export-markdown.ts';
import { createApi } from '../src/api/index.ts';
import {
  exportFileName,
  placeTurns,
  SESSION_EXPORT_PAGE,
  type SessionExportDeps,
  sessionExport,
} from '../src/api/session-export.ts';
import { createControl } from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';
import { assistant, userResult, userText } from './fixtures/claude-grammar.ts';

const HOST = 'host-token-0123456789abcdef';
const SESSION = 'exp-s1';
const dirs: string[] = [];
const servers: Server[] = [];

afterEach(async () => {
  for (const s of servers.splice(0)) {
    s.closeAllConnections();
    await new Promise<void>((r) => s.close(() => r()));
  }
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

const T = (min: number) => new Date(Date.UTC(2026, 9, 24, 13, min)).toISOString();

const LINES = [
  userText('Fix the flaky reconnect test', T(0)),
  assistant([{ type: 'text', text: 'Looking at the test first.' }], T(1)),
  assistant(
    [
      {
        type: 'tool_use',
        id: 'r1',
        name: 'Read',
        input: { file_path: 'core/test/reconnect.test.ts' },
      },
      { type: 'tool_use', id: 'b1', name: 'Bash', input: { command: 'pnpm vitest run reconnect' } },
    ],
    T(2),
  ),
  userResult('r1', 'file body', T(2)),
  userResult('b1', 'Exit code 1\n1 failed', T(3), true),
  assistant([{ type: 'text', text: 'The timer was real. Fixed with a fake clock.' }], T(4)),
  userText('Now run it with HOPLET_TOKEN=hpt_abcdefghijklmnopqrstuv please', T(20)),
  assistant(
    [{ type: 'tool_use', id: 'b2', name: 'Bash', input: { command: 'pnpm vitest run' } }],
    T(21),
  ),
  userResult('b2', 'all 12 passed', T(22)),
  assistant([{ type: 'text', text: 'All green on project-zebra.' }], T(23)),
];

function open(lines: unknown[] = LINES) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-export-'));
  dirs.push(dir);
  const store = new Store(dir);
  store.upsertDevice('dev-a', 'MacBook Pro', '0.0.0');
  store.upsertDevice('dev-b', 'studio', '0.0.0');
  store.append(
    {
      sessionId: SESSION,
      provider: 'claude',
      projectDir: 'p',
      filePath: '/x/s1',
      deviceId: 'dev-a',
    },
    lines.map((l) => JSON.stringify(l)),
  );
  return { dir, store };
}

type FakeChanges = { files: WorkspaceChange[]; diffs: Record<string, string | 'binary'> };

function fakeControl(changes?: FakeChanges): SessionExportDeps['control'] {
  return {
    ownerOf: () => (changes ? 'dev-a' : null),
    workspaceChanges: async (sessionId) =>
      changes
        ? {
            ok: true,
            changes: {
              sessionId,
              branch: 'fix/reconnect',
              base: '0123456789abcdef',
              shared: false,
              total: changes.files.length,
              files: changes.files,
            },
          }
        : { ok: false, error: 'owner-offline' },
    workspaceDiff: async (sessionId, _owner, p, page) => {
      const d = changes?.diffs[p];
      if (d === undefined) return { ok: false, error: 'rejected' };
      if (d === 'binary') {
        return { ok: true, diff: { kind: 'binary', sessionId, path: p, status: 'modified' } };
      }
      const limit = page?.limit ?? EXPORT_DIFF_MAX_BYTES;
      return {
        ok: true,
        diff: {
          kind: 'text',
          sessionId,
          path: p,
          status: 'modified',
          diff: d.slice(0, limit),
          truncated: d.length > limit,
          size: d.length,
        },
      };
    },
  };
}

function query(q: Partial<SessionExportQuery> = {}): SessionExportQuery {
  return { conversation: 'all', changes: 'false', commands: 'false', machines: 'false', ...q };
}

/** Serves one export on a local server and returns the response. */
async function serve(deps: SessionExportDeps, q: SessionExportQuery) {
  const raw = sessionExport(deps, SESSION, q);
  const server = createServer((_req, res) => {
    void raw.send(res);
  });
  servers.push(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  const port = (server.address() as AddressInfo).port;
  return fetch(`http://127.0.0.1:${port}/`);
}

const now = () => Date.UTC(2026, 9, 24, 15, 0);

describe('the Markdown of an export', () => {
  it('writes front matter, the warning, the table and each turn with its tool calls', async () => {
    const { store } = open();
    const res = await serve({ store, control: fakeControl(), now }, query());
    expect(res.status).toBe(200);
    expect(res.headers.get('content-type')).toBe('text/markdown; charset=utf-8');
    expect(res.headers.get('content-disposition')).toMatch(/^attachment; filename=".+\.md"$/);
    const text = await res.text();
    expect(text.startsWith('---\ntitle: ')).toBe(true);
    expect(text).toContain(`session: ${SESSION}\n`);
    expect(text).toContain('provider: claude\n');
    expect(text).toContain('turns: 2\n');
    expect(text).toContain('exported: 2026-10-24T15:00:00Z\n');
    expect(text).toContain('> **Check before sharing.**');
    expect(text).toContain('| Turns | 2 · 23 min |');
    expect(text).toContain('## Conversation');
    expect(text).toContain('### Turn 1 · 13:00\n\n> **You:** Fix the flaky reconnect test');
    expect(text).toContain('<details><summary>2 tool calls</summary>');
    expect(text).toContain('- Read `core/test/reconnect.test.ts`');
    expect(text).toContain('- Ran `pnpm vitest run reconnect` · exit 1');
    expect(text).toContain('```text\n  Exit code 1\n  1 failed\n  ```');
    expect(text).toContain('The timer was real. Fixed with a fake clock.');
    expect(text).not.toContain('## Commands');
    expect(text).not.toContain('## Changes');
  });

  it('keeps only the messages and the last reply in replies mode, and no turns in none', async () => {
    const { store } = open();
    const replies = await (
      await serve({ store, control: fakeControl(), now }, query({ conversation: 'replies' }))
    ).text();
    expect(replies).toContain('> **You:** Fix the flaky reconnect test');
    expect(replies).toContain('The timer was real.');
    expect(replies).not.toContain('Looking at the test first.');
    expect(replies).not.toContain('<details>');

    const none = await (
      await serve(
        { store, control: fakeControl(), now },
        query({ conversation: 'none', summary: 'We fixed the test.' }),
      )
    ).text();
    expect(none).toContain('## Summary\n\nWe fixed the test.');
    expect(none).not.toContain('## Conversation');
    expect(none).not.toContain('### Turn');
    expect(none).toContain('turns: 2\n');
  });

  it('writes the times in the time zone asked for', async () => {
    const { store } = open();
    const text = await (
      await serve({ store, control: fakeControl(), now }, query({ tz: 'Asia/Shanghai' }))
    ).text();
    expect(text).toContain('### Turn 1 · 21:00');
  });

  it('lists the commands with their turn and exit code', async () => {
    const { store } = open();
    const text = await (
      await serve(
        { store, control: fakeControl(), now },
        query({ conversation: 'none', commands: 'true' }),
      )
    ).text();
    expect(text).toContain('## Commands\n\n| Turn | Command | Exit |');
    expect(text).toContain('| 1 | `pnpm vitest run reconnect` | 1 |');
    expect(text).toContain('| 2 | `pnpm vitest run` | 0 |');
  });

  it('names the machines and the hand-off point', async () => {
    const { store } = open();
    store.audit({
      type: 'session.handoff',
      sessionId: SESSION,
      deviceId: 'dev-b',
      at: Date.parse(T(10)),
      summary: { from: 'dev-a', to: 'dev-b', outcome: 'committed' },
    });
    store.audit({
      type: 'session.handoff',
      sessionId: SESSION,
      deviceId: 'dev-a',
      at: Date.parse(T(11)),
      summary: { from: 'dev-b', to: 'dev-a', outcome: 'failed' },
    });
    const text = await (
      await serve(
        { store, control: fakeControl(), now },
        query({ machines: 'true', commands: 'true' }),
      )
    ).text();
    expect(text).toContain('machines: ["MacBook Pro", "studio"]');
    expect(text).toContain('| Machines | MacBook Pro (turn 1) → studio (turn 2) |');
    expect(text).toContain('| Hand-off | turn 2, 2026-10-24 13:10 |');
    expect(text).toContain('---\n\n*Handed off from MacBook Pro to studio at turn 2 · 13:10.*');
    expect(text).toContain('### Turn 2 · studio · 13:20');
    expect(text).toContain('| Turn | Machine | Command | Exit |');
    expect(text).toContain('| 2 | studio | `pnpm vitest run` | 0 |');
  });

  it('writes the net diff, a note for a binary file, and stops at the cap', async () => {
    const { store } = open();
    const big = `${'+x\n'.repeat(EXPORT_DIFF_MAX_BYTES / 3 + 10)}`;
    const changes: FakeChanges = {
      files: [
        { path: 'a.ts', status: 'modified', added: 1, removed: 1 },
        { path: 'logo.png', status: 'modified', binary: true },
        { path: 'big.txt', status: 'added', added: 90_000, removed: 0 },
        { path: 'z.ts', status: 'modified', added: 1, removed: 0 },
      ],
      diffs: {
        'a.ts': '@@ -1 +1 @@\n-a\n+b\n',
        'logo.png': 'binary',
        'big.txt': big,
        'z.ts': '+z\n',
      },
    };
    const text = await (
      await serve(
        { store, control: fakeControl(changes), now },
        query({ conversation: 'none', changes: 'true' }),
      )
    ).text();
    expect(text).toContain('| Turns | 2 · 23 min · +90002 −1 in 4 files |');
    expect(text).toContain('Net diff against `0123456789ab` at turn 2 · 4 files · +90002 −1.');
    expect(text).toContain('```diff\n--- a/a.ts\n+++ b/a.ts\n@@ -1 +1 @@\n-a\n+b\n');
    expect(text).toContain('Binary files a/logo.png and b/logo.png differ');
    expect(text).toContain('--- /dev/null\n+++ b/big.txt\n');
    expect(text).toContain('*The diff of `big.txt` is cut at 256 KiB.*');
    expect(text).toContain('*1 more file is not shown: the diff stops at 256 KiB.*');
    expect(text).not.toContain('+++ b/z.ts');
    const diff = text.slice(text.indexOf('```diff'));
    expect(Buffer.byteLength(diff)).toBeLessThan(EXPORT_DIFF_MAX_BYTES + 4096);
  });

  it('says so when the changes cannot be read', async () => {
    const { store } = open();
    const text = await (
      await serve({ store, control: fakeControl(), now }, query({ changes: 'true' }))
    ).text();
    expect(text).toContain('## Changes\n\n*The changes could not be read:');
  });
});

describe('redaction', () => {
  it('masks credentials by shape and the strings the user adds, and counts nothing it should not', async () => {
    const { store } = open();
    const text = await (
      await serve(
        { store, control: fakeControl(), now },
        query({ mask: ['project-zebra'], summary: 'Key sk-abcdefghijklmnopqrstuvwx leaked' }),
      )
    ).text();
    expect(text).toContain('HOPLET_TOKEN=[redacted:hoplet-token] please');
    expect(text).toContain('All green on [redacted:custom].');
    expect(text).toContain('Key [redacted:api-key] leaked');
    expect(text).not.toContain('hpt_abcdefghijklmnopqrstuv');
    expect(text).not.toContain('project-zebra');
    // The review warning names the marker and is not itself masked.
    expect(text).toContain('shown as `[redacted:<kind>]`');
  });
});

describe('streaming and limits', () => {
  it('reads a long session in pages and writes every turn', async () => {
    const lines: unknown[] = [];
    const turns = Math.ceil((SESSION_EXPORT_PAGE * 2.5) / 2);
    for (let i = 0; i < turns; i++) {
      lines.push(
        userText(`prompt ${i}`, T(0)),
        assistant([{ type: 'text', text: `reply ${i}` }], T(1)),
      );
    }
    const { store } = open(lines);
    const res = await serve({ store, control: fakeControl(), now }, query());
    const text = await res.text();
    expect(text).toContain(`turns: ${turns}\n`);
    expect(text.match(/^### Turn /gm)).toHaveLength(turns);
    expect(text).toContain(`reply ${turns - 1}`);
  });

  it('stops when the client goes away and frees its place', async () => {
    const lines: unknown[] = [];
    for (let i = 0; i < 3000; i++) {
      lines.push(userText(`prompt ${i} ${'w'.repeat(2000)}`, T(0)));
    }
    const { store } = open(lines);
    const deps = { store, control: fakeControl(), now };
    const controller = new AbortController();
    const raw = sessionExport(deps, SESSION, query());
    let done: Promise<void> = Promise.resolve();
    const server = createServer((_req, res) => {
      done = raw.send(res);
    });
    servers.push(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    const port = (server.address() as AddressInfo).port;
    const res = await fetch(`http://127.0.0.1:${port}/`, { signal: controller.signal });
    const reader = res.body?.getReader();
    await reader?.read();
    controller.abort();
    await done;
    // Two more exports fit: the aborted one gave its place back.
    sessionExport(deps, SESSION, query());
    sessionExport(deps, SESSION, query());
  });

  it('refuses a third export at once and records each one it starts', () => {
    const { store } = open();
    const deps = { store, control: fakeControl(), now };
    sessionExport(deps, SESSION, query({ commands: 'true', mask: ['secret-a', 'secret-b'] }));
    sessionExport(deps, SESSION, query());
    expect(() => sessionExport(deps, SESSION, query())).toThrow(
      expect.objectContaining({ code: 'rate-limited' }),
    );
    const events: AuditEvent[] = store.listAudit(
      { type: 'session.exported' },
      { order: 'asc', limit: 10 },
    ).events;
    expect(events).toHaveLength(2);
    expect(events[0]).toMatchObject({
      sessionId: SESSION,
      summary: {
        conversation: 'all',
        commands: true,
        changes: false,
        machines: false,
        summary: false,
        masks: 2,
      },
    });
    expect(JSON.stringify(events[0])).not.toContain('secret-a');
  });

  it('answers session-not-found for an unknown session', () => {
    const { store } = open();
    expect(() => sessionExport({ store, control: fakeControl(), now }, 'nope', query())).toThrow(
      expect.objectContaining({ code: 'session-not-found' }),
    );
  });

  it('bounds the query', () => {
    const ok = (q: Record<string, string>) => safeParse(SessionExportQuerySchema, q).success;
    expect(ok({})).toBe(true);
    expect(ok({ conversation: 'everything' })).toBe(false);
    expect(ok({ summary: 'x'.repeat(3 * 1024 + 1) })).toBe(false);
    expect(ok({ mask: 'ab' })).toBe(false);
    expect(ok({ mask: Array.from({ length: 21 }, (_, i) => `term-${i}`).join('\n') })).toBe(false);
    expect(ok({ mask: 'x'.repeat(1025) })).toBe(false);
    expect(ok({ tz: 'Mars/Olympus' })).toBe(false);
    expect(ok({ tz: 'Europe/Berlin', changes: 'true', mask: ' alpha \n\nbeta ' })).toBe(true);
    const parsed = safeParse(SessionExportQuerySchema, { mask: ' alpha \n\nbeta ' });
    expect(parsed.success && parsed.output.mask).toEqual(['alpha', 'beta']);
  });
});

describe('the route', () => {
  async function boot() {
    const { dir, store } = open();
    const server = createServer();
    createApi(
      store,
      { port: 0, dataDir: dir, token: HOST, webDir: null },
      createControl(store),
    ).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const port = (server.address() as AddressInfo).port;
    const get = (url: string, auth = true) =>
      fetch(`http://127.0.0.1:${port}${url}`, {
        headers: auth ? { authorization: `Bearer ${HOST}` } : {},
      });
    return { get };
  }

  it('needs the host token, validates the query and streams the file', async () => {
    const { get } = await boot();
    expect((await get(`/api/sessions/${SESSION}/export`, false)).status).toBe(401);
    expect((await get(`/api/sessions/${SESSION}/export?conversation=x`)).status).toBe(400);
    expect((await get('/api/sessions/nope/export')).status).toBe(404);
    const res = await get(`/api/sessions/${SESSION}/export?commands=true&mask=project-zebra`);
    expect(res.status).toBe(200);
    const text = await res.text();
    expect(text).toContain('## Commands');
    expect(text).toContain('[redacted:custom]');
  });
});

describe('the parts', () => {
  it('names the file after the title', () => {
    expect(exportFileName('Fix the flaky reconnect test!')).toBe('fix-the-flaky-reconnect-test.md');
    expect(exportFileName('修复')).toBe('session.md');
  });

  it('places turns on machines from the hand-offs', () => {
    const at = (m: number) => Date.parse(T(m));
    const placed = placeTurns(
      [T(0), T(5), T(15), T(25)],
      [
        { at: at(10), from: 'a', to: 'b' },
        { at: at(20), from: 'b', to: 'a' },
      ],
      'a',
    );
    expect(placed.machineOf).toEqual(['a', 'a', 'b', 'a']);
    expect(placed.points.map((p) => p.turn)).toEqual([3, 4]);
  });

  it('fences output past backticks inside it and elides a long one', () => {
    const line = toolLine({
      id: 'x',
      kind: 'run',
      name: 'Bash',
      arg: 'echo',
      result: { isError: false, exit: 0, bytes: 9, text: '```\nhi\n```' },
    });
    expect(line).toContain('  ````text\n  ```\n  hi\n  ```\n  ````');
    const elided = toolLine({
      id: 'y',
      kind: 'run',
      name: 'Bash',
      arg: 'cat big',
      result: { isError: false, exit: 0, bytes: 20_000 },
    });
    expect(elided).toContain('- Output elided: 20 KiB, see the session in Hoplet');
  });

  it('starts a turn at each message and keeps steering inside the turn', () => {
    const b = new TurnBuilder('/w');
    expect(
      b.add({
        lineNo: 0,
        role: 'user',
        blocks: [{ type: 'text', text: 'go' }],
      } as never),
    ).toBeNull();
    b.add({
      lineNo: 1,
      role: 'user',
      steered: true,
      blocks: [{ type: 'text', text: 'also this' }],
    } as never);
    const ended = b.add({
      lineNo: 2,
      role: 'user',
      blocks: [{ type: 'text', text: 'next' }],
    } as never);
    expect(ended).toMatchObject({
      n: 1,
      prompt: 'go',
      items: [{ type: 'steer', text: 'also this' }],
    });
    expect(b.finish()).toMatchObject({ n: 2, prompt: 'next' });
  });

  it('quotes the title in the front matter', () => {
    const head = renderHeader({
      title: 'A "quoted"\ntitle',
      sessionId: 's',
      project: 'p',
      provider: 'claude',
      turns: 0,
      exported: 'now',
      version: '0.0.0',
      conversation: 'all',
    });
    expect(head).toContain('title: "A \\"quoted\\"\\ntitle"');
    expect(head).toContain('# A "quoted" title');
  });
});
