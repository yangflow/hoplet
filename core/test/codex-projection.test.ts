import { mkdtempSync, rmSync } from 'node:fs';
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { EntrySchema, is, LinesPageSchema, safeParse } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { createApi } from '../src/api/index.ts';
import {
  classifyTool,
  ELIDE_ABOVE_BYTES,
  fullEntry,
  projectLine,
} from '../src/api/projection/index.ts';
import { createControl } from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';
import { extractMeta } from '../src/store/title.ts';
import {
  agentItem,
  BIG_OUTPUT,
  codexRollout,
  commandItem,
  item,
  record,
  sessionMeta,
  userItem,
} from './fixtures/codex-rollout.ts';

const cleanups: (() => void)[] = [];
afterEach(() => {
  for (const cleanup of cleanups.splice(0)) cleanup();
});

const map = (line: unknown, no = 0) => projectLine('codex', line, no);

describe('Codex mapping', () => {
  it('maps a user message, an agent message and a plan from their items', () => {
    expect(map(userItem('hello there'))).toMatchObject({
      role: 'user',
      ts: '2026-10-05T09:00:00.000Z',
      blocks: [{ type: 'text', text: 'hello there' }],
    });
    expect(map(agentItem('done', 'commentary'))).toMatchObject({
      role: 'assistant',
      blocks: [{ type: 'text', text: 'done' }],
    });
    expect(map(item({ type: 'Plan', id: 'p', text: '1. one\n2. two\n' }))?.blocks).toEqual([
      { type: 'text', text: '1. one\n2. two\n' },
    ]);
  });

  it('keeps images of a user message and names content parts it does not know', () => {
    const entry = map(
      item({
        type: 'UserMessage',
        content: [
          { type: 'text', text: 'look' },
          { type: 'image', image_url: 'data:image/png;base64,AAAA' },
          { type: 'image', image_url: 'https://x.test/a.png' },
          { type: 'local_image', path: '/tmp/a.png' },
          { type: 'mention', path: '/x' },
        ],
      }),
    );
    expect(entry?.blocks).toEqual([
      { type: 'text', text: 'look' },
      { type: 'image', mediaType: 'image/png', data: 'AAAA' },
      { type: 'image', mediaType: 'image', url: 'https://x.test/a.png' },
      { type: 'image', mediaType: 'image', url: '/tmp/a.png' },
      { type: 'other', kind: 'mention', text: '{"type":"mention","path":"/x"}' },
    ]);
  });

  it('shows reasoning only when it has a summary', () => {
    expect(map(item({ type: 'Reasoning', summary_text: [], raw_content: [] }))).toBeNull();
    expect(map(item({ type: 'Reasoning', summary_text: ['Weighing', 'options'] }))?.blocks).toEqual(
      [{ type: 'thinking', text: 'Weighing\n\noptions' }],
    );
  });

  it('maps a command with its output as a run call and result', () => {
    expect(map(commandItem())?.blocks).toEqual([
      {
        type: 'tool-call',
        id: 'cmd1',
        name: 'exec_command',
        kind: 'run',
        input: { command: 'git status --short', cwd: '/work/proj' },
      },
      { type: 'tool-result', id: 'cmd1', content: ' M a.ts\n' },
    ]);
  });

  it('marks a failed or declined command as an error and names its exit code', () => {
    const failed = map(commandItem({ status: 'failed', exit_code: 2, aggregated_output: 'no' }));
    expect(failed?.blocks[1]).toEqual({
      type: 'tool-result',
      id: 'cmd1',
      content: 'Exit code 2\nno',
      isError: true,
    });
    const declined = map(
      commandItem({ status: 'declined', exit_code: undefined, aggregated_output: '' }),
    );
    expect(declined?.blocks[1]).toMatchObject({ content: 'declined', isError: true });
    const argv = map(commandItem({ command: ['git', 'log'] }));
    expect(argv?.blocks[0]).toMatchObject({ input: { command: 'git log' } });
  });

  it('maps file changes to write and edit calls, one per file', () => {
    const entry = map(
      item({
        type: 'FileChange',
        id: 'fc',
        status: 'completed',
        stdout: 'Success',
        stderr: '',
        changes: {
          '/work/proj/new.ts': { type: 'add', content: 'export {};\n' },
          '/work/proj/a.ts': {
            type: 'update',
            unified_diff: '@@ -1,2 +1,2 @@\n keep\n-old\n+new\n',
            move_path: null,
            extra: 1,
          },
          '/work/proj/gone.ts': { type: 'delete' },
        },
      }),
    );
    expect(entry?.blocks).toEqual([
      {
        type: 'tool-call',
        id: 'fc#0',
        name: 'apply_patch',
        kind: 'write',
        input: { file_path: '/work/proj/new.ts', content: 'export {};\n' },
      },
      { type: 'tool-result', id: 'fc#0', content: 'Success' },
      {
        type: 'tool-call',
        id: 'fc#1',
        name: 'apply_patch',
        kind: 'edit',
        input: {
          file_path: '/work/proj/a.ts',
          old_string: 'keep\nold',
          new_string: 'keep\nnew',
        },
      },
      { type: 'tool-result', id: 'fc#1', content: 'Success' },
      {
        type: 'tool-call',
        id: 'fc#2',
        name: 'apply_patch',
        kind: 'edit',
        input: { file_path: '/work/proj/gone.ts', delete: true },
      },
      { type: 'tool-result', id: 'fc#2', content: 'Success' },
    ]);
    expect(map(item({ type: 'FileChange', id: 'x', changes: {} }))).toBeNull();
  });

  it('maps an MCP call to the call kind with its server and tool', () => {
    const entry = map(
      item({
        type: 'McpToolCall',
        id: 'm1',
        server: 'docs',
        tool: 'search',
        arguments: { q: 'x' },
        status: 'completed',
        result: { content: [{ type: 'text', text: 'found' }, { type: 'image' }], isError: false },
      }),
    );
    expect(entry?.blocks).toEqual([
      {
        type: 'tool-call',
        id: 'm1',
        name: 'mcp__docs__search',
        kind: 'call',
        mcp: { server: 'docs', tool: 'search' },
        input: { q: 'x' },
      },
      { type: 'tool-result', id: 'm1', content: 'found\n[image]' },
    ]);
    const failed = map(
      item({
        type: 'McpToolCall',
        id: 'm2',
        server: 's',
        tool: 't',
        result: { content: [], isError: true },
      }),
    );
    expect(failed?.blocks[1]).toMatchObject({ isError: true });
  });

  it('maps a web search to a search call and any other extension to other', () => {
    const search = map(
      item({
        type: 'Extension',
        id: 'e1',
        kind: 'web_search',
        query: 'vitest',
        action: { type: 'open', url: 'https://vitest.dev' },
        results: [],
      }),
    );
    expect(search?.blocks[0]).toEqual({
      type: 'tool-call',
      id: 'e1',
      name: 'web_search',
      kind: 'search',
      input: { query: 'vitest', url: 'https://vitest.dev' },
    });
    expect(map(item({ type: 'Extension', id: 'e2', kind: 'image_gen' }))?.blocks[0]).toMatchObject({
      name: 'image_gen',
      kind: 'other',
    });
  });

  it('shows an interrupted turn as a system line', () => {
    const entry = map(
      record('event_msg', { type: 'turn_aborted', reason: 'interrupted', turn_id: 'u1' }),
    );
    expect(entry).toMatchObject({
      role: 'system',
      blocks: [{ type: 'text', text: 'Turn interrupted' }],
    });
    expect(map(record('event_msg', { type: 'turn_aborted', reason: 'replaced' }))?.blocks).toEqual([
      { type: 'text', text: 'Turn aborted: replaced' },
    ]);
  });

  it('ignores records and items it does not know', () => {
    const ignored: unknown[] = [
      sessionMeta(),
      record('turn_context', { cwd: '/x' }),
      record('token_usage_record', { total: 1 }),
      record('world_state', {}),
      record('future_record', { a: 1 }),
      record('response_item', {
        type: 'message',
        role: 'user',
        content: [{ type: 'input_text', text: 'raw' }],
      }),
      record('response_item', { type: 'custom_tool_call', name: 'exec', input: 'tools.x()' }),
      record('event_msg', { type: 'task_started' }),
      record('event_msg', { type: 'task_complete', last_agent_message: 'x' }),
      record('event_msg', { type: 'token_count' }),
      record('event_msg', { type: 'thread_settings_applied' }),
      record('event_msg', { type: 'future_event' }),
      item({ type: 'FutureItem', id: 'f' }),
      item({ type: 'AgentMessage', content: [] }),
      { type: 'event_msg' },
      { type: 'event_msg', payload: { type: 'item_completed', item: 'nope' } },
      'text',
      null,
      [],
    ];
    ignored.forEach((line, i) => {
      expect(map(line, i)).toBeNull();
    });
  });

  it('keeps an unparsed line visible, as for Claude', () => {
    expect(map({ _parseError: true, raw: '{bad' })?.blocks).toEqual([
      { type: 'other', kind: 'unparsed', text: '{bad' },
    ]);
  });

  it('produces entries the shared schema accepts and shows each message once', () => {
    const entries = codexRollout().flatMap((l, i) => map(l, i) ?? []);
    for (const e of entries) expect(is(EntrySchema, e)).toBe(true);
    expect(entries.map((e) => e.role)).toEqual(['user', 'assistant', 'assistant']);
    expect(JSON.stringify(entries)).not.toContain('AGENTS.md');
  });

  it('classifies Codex tools', () => {
    expect(classifyTool('codex', 'exec_command')).toEqual({ kind: 'run' });
    expect(classifyTool('codex', 'apply_patch')).toEqual({ kind: 'edit' });
    expect(classifyTool('codex', 'mcp__docs__search')).toEqual({
      kind: 'call',
      mcp: { server: 'docs', tool: 'search' },
    });
    expect(classifyTool('codex', 'whatever')).toEqual({ kind: 'other' });
  });

  it('elides a large command output and keeps it whole in the full entry', () => {
    const full = fullEntry('codex', commandItem({ aggregated_output: BIG_OUTPUT }), 0);
    expect(full?.blocks[1]).toMatchObject({ content: BIG_OUTPUT });
    const out = map(commandItem({ aggregated_output: BIG_OUTPUT }), 4);
    expect(out?.blocks[1]).toMatchObject({ elided: true, type: 'tool-result', ref: '4/1' });
  });
});

describe('Codex titles', () => {
  const lines = (...ls: unknown[]) => ls.map((l) => JSON.stringify(l));

  it('takes the title from the first user message and the rest from session_meta', () => {
    const meta = extractMeta(
      'codex',
      lines(
        record('session_meta', { cwd: '/work/proj', timestamp: '2026-05-01T08:00:00.000Z' }),
        userItem('/status'),
        userItem('Fix the cursor drift in the sync loop'),
        userItem('another later prompt about something else'),
      ),
    );
    expect(meta.title).toBe('Fix the cursor drift in the sync loop');
    expect(meta.projectPath).toBe('/work/proj');
    expect(meta.createdAt).toBe(Date.parse('2026-05-01T08:00:00.000Z'));
  });

  it('has no title before a user message', () => {
    expect(extractMeta('codex', lines(sessionMeta())).title).toBe('');
  });
});

describe('Codex sessions through the read API', () => {
  it('pages entries and never serves the session_meta line', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-codex-'));
    const store = new Store(dir);
    store.append(
      { sessionId: 't1', provider: 'codex', projectDir: 'p', filePath: '/x', deviceId: 'd' },
      codexRollout().map((l) => JSON.stringify(l)),
    );
    const server = createServer();
    createApi(
      store,
      { port: 0, dataDir: dir, token: 'host-token', webDir: null },
      createControl(store),
    ).attach(server);
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
    cleanups.push(() => {
      server.close();
      server.closeAllConnections();
      rmSync(dir, { force: true, recursive: true });
    });
    const port = (server.address() as AddressInfo).port;
    const get = (url: string) =>
      fetch(`http://127.0.0.1:${port}${url}`, { headers: { authorization: 'Bearer host-token' } });

    const res = await get('/api/sessions/t1/lines?start=0&limit=100');
    expect(res.status).toBe(200);
    const body = await res.text();
    const parsed = safeParse(LinesPageSchema, JSON.parse(body));
    expect(parsed.success).toBe(true);
    if (!parsed.success) return;
    expect(parsed.output.total).toBe(codexRollout().length);
    expect(parsed.output.entries.map((e) => e.lineNo)).toEqual([5, 7, 9]);
    expect(body).not.toContain('xxxxxxxxxx');
    expect(body.length).toBeLessThan(ELIDE_ABOVE_BYTES * 2);
    expect(parsed.output.entries[1]?.blocks[1]).toMatchObject({
      elided: true,
      type: 'tool-result',
      ref: '7/1',
    });

    const block = await (await get('/api/sessions/t1/blocks/7/1')).json();
    expect(block).toMatchObject({ type: 'tool-result', content: BIG_OUTPUT });
    expect((await get('/api/sessions/t1/blocks/0/0')).status).toBe(404);
  });
});
