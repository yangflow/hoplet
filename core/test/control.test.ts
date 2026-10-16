import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION, type ServerMessage } from '@hoplet/shared';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApi } from '../src/api/index.ts';
import {
  type ControlOptions,
  type ControlReply,
  createControl,
  type DeviceLink,
} from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
const servers: Server[] = [];

afterEach(async () => {
  vi.restoreAllMocks();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

/** A `runtime.state` message of an agent. */
function agentState(sessionId: string, state: string, error?: string) {
  return {
    v: PROTOCOL_VERSION,
    type: 'runtime.state',
    sessionId,
    state,
    ...(error === undefined ? {} : { error }),
    capabilities: { permissions: true, interrupt: true, config: [] },
  } as ControlReply;
}

/** A device link that records what control sends. */
function fakeLink(): DeviceLink & { sent: ServerMessage[]; open: boolean } {
  const link = {
    sent: [] as ServerMessage[],
    open: true,
    send(msg: ServerMessage) {
      link.sent.push(msg);
    },
    async sendAndWait(msg: ServerMessage) {
      link.sent.push(msg);
    },
    isOpen: () => link.open,
  };
  return link;
}

function setup(options: ControlOptions = { promptTimeoutMs: 100, workspaceTimeoutMs: 100 }) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-control-'));
  dirs.push(dir);
  const store = new Store(dir);
  store.append({ sessionId: 's1', projectDir: 'p', filePath: '/x', deviceId: 'dev-a' }, [
    '{"type":"user"}',
  ]);
  return { store, control: createControl(store, options) };
}

describe('device RPC', () => {
  it('forwards a prompt to the owner and resolves when the agent reports the session running', async () => {
    const { control } = setup();
    const link = fakeLink();
    control.attach('dev-a', link);
    const pending = control.prompt('s1', 'dev-a', 'do the thing', 'p1');
    const sent = link.sent[0];
    expect(sent).toMatchObject({ type: 'runtime.prompt', promptId: 'p1', text: 'do the thing' });
    control.receive(link, agentState('s1', 'running'));
    expect(await pending).toEqual({ ok: true, promptId: 'p1' });
  });

  it('logs a prompt with session and prompt ids, never its text', async () => {
    const { control } = setup();
    const lines: string[] = [];
    vi.spyOn(console, 'log').mockImplementation((line: string) => {
      lines.push(String(line));
    });
    const link = fakeLink();
    control.attach('dev-a', link);
    const outcome = control.prompt('s1', 'dev-a', 'SECRET-PROMPT-TEXT', 'p2');
    const promptId = (link.sent[0] as { promptId: string }).promptId;
    control.receive(link, agentState('s1', 'running'));
    await outcome;
    const entry = lines.map((l) => JSON.parse(l)).find((e) => e.message === 'prompt forwarded');
    expect(entry).toMatchObject({ module: 'control', sessionId: 's1', promptId });
    expect(lines.join('\n')).not.toContain('SECRET-PROMPT-TEXT');
  });

  it('answers owner-offline without sending, and timeout when the agent stays silent', async () => {
    const { control } = setup();
    expect(await control.prompt('s1', 'dev-a', 'x', 'p')).toMatchObject({
      ok: false,
      error: 'owner-offline',
    });
    const link = fakeLink();
    control.attach('dev-a', link);
    expect(await control.workspace('s1', 'dev-a')).toEqual({ ok: false, error: 'timeout' });
    expect(link.sent).toHaveLength(1);
    link.open = false;
    expect(control.ownerOnline('dev-a')).toBe(false);
  });

  it('resolves a device project request on the reply', async () => {
    const { control } = setup();
    const owner = fakeLink();
    control.attach('dev-a', owner);
    const pending = control.listProjects('dev-a');
    const requestId = (owner.sent[0] as { requestId: string }).requestId;
    control.receive(owner, {
      v: PROTOCOL_VERSION,
      type: 'device.projects',
      requestId,
      candidates: [],
      projects: [],
    });
    expect(await pending).toEqual({ ok: true, candidates: [], projects: [] });
  });

  it('forgets a link when it detaches', () => {
    const { control } = setup();
    const link = fakeLink();
    control.attach('dev-a', link);
    expect(control.ownerOnline('dev-a')).toBe(true);
    control.detach(link);
    expect(control.ownerOnline('dev-a')).toBe(false);
  });
});

describe('the route table around the handlers', () => {
  async function boot() {
    const { store, control } = setup();
    const config = { port: 0, dataDir: '', token: 'host', webDir: null };
    const server = createServer();
    createApi(store, config, control).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const call = (method: string, p: string, body?: string, token = 'host') =>
      fetch(base + p, {
        method,
        headers: token ? { authorization: `Bearer ${token}` } : {},
        ...(body === undefined ? {} : { body }),
      });
    return { control, call };
  }

  it('requires the host token on every /api path, known or not', async () => {
    const { call } = await boot();
    for (const p of ['/api/health', '/api/nope']) {
      const res = await call('GET', p, undefined, '');
      expect(res.status).toBe(401);
      expect(await res.json()).toEqual({ code: 'unauthorized' });
    }
    expect((await call('GET', '/api/health', undefined, 'wrong')).status).toBe(401);
  });

  it('answers 405 with Allow for a known path and the wrong method', async () => {
    const { call } = await boot();
    const res = await call('DELETE', '/api/devices');
    expect(res.status).toBe(405);
    expect(res.headers.get('allow')).toBe('GET, POST');
    expect(await res.json()).toEqual({ code: 'method-not-allowed' });
    expect((await call('GET', '/api/nope')).status).toBe(404);
  });

  it('validates bodies and limits them before the handler runs', async () => {
    const { call } = await boot();
    const prompt = '/api/sessions/s1/prompt';
    expect((await call('POST', prompt, 'not json')).status).toBe(400);
    expect(await (await call('POST', prompt, '{}')).json()).toEqual({ code: 'invalid-request' });
    const big = await call(
      'POST',
      prompt,
      JSON.stringify({ text: 'x'.repeat(40 * 1024), promptId: 'p' }),
    );
    expect(big.status).toBe(413);
    expect(await big.json()).toEqual({ code: 'too-large' });
  });

  it('maps control outcomes to their codes and statuses', async () => {
    const { control, call } = await boot();
    const prompt = '/api/sessions/s1/prompt';
    const body = JSON.stringify({ text: 'hi', promptId: 'p1' });
    const offline = await call('POST', prompt, body);
    expect(offline.status).toBe(409);
    expect(await offline.json()).toEqual({ code: 'owner-offline' });
    expect((await call('POST', '/api/sessions/zz/prompt', body)).status).toBe(404);

    const link = fakeLink();
    control.attach('dev-a', link);
    const timeout = await call('POST', prompt, body);
    expect(timeout.status).toBe(504);
    expect(await timeout.json()).toMatchObject({ code: 'timeout' });
  });
});

describe('remote control state', () => {
  const caps = { permissions: true, interrupt: true, config: [] };

  function runtimeSetup(now?: () => number) {
    const { store, control } = setup({
      promptTimeoutMs: 100,
      workspaceTimeoutMs: 100,
      ...(now ? { now } : {}),
    });
    const link = fakeLink();
    control.attach('dev-a', link);
    const events: ServerMessage[] = [];
    control.subscribe((msg) => events.push(msg));
    const report = (state: string, error?: string) =>
      control.receive(link, agentState('s1', state, error));
    return { store, control, link, events, report };
  }

  const kinds = (events: ServerMessage[]) =>
    events.map((e) => (e.type === 'session.runtime' ? `${e.type}:${e.state}` : e.type));

  it('shows offline until the owner connects, idle after, and what the agent reports', () => {
    const { control } = setup();
    expect(control.runtimeOf('s1')).toEqual({ state: 'offline' });
    const link = fakeLink();
    control.attach('dev-a', link);
    expect(control.runtimeOf('s1')).toEqual({ state: 'idle' });
    control.receive(link, agentState('s1', 'ready'));
    expect(control.runtimeOf('s1')).toEqual({ state: 'ready', capabilities: caps });
    control.receive(link, agentState('s1', 'failed', 'exit 3'));
    expect(control.runtimeOf('s1')).toEqual({
      state: 'failed',
      error: 'exit 3',
      capabilities: caps,
    });
  });

  it('publishes session.runtime for every report, with the capabilities', () => {
    const { events, report } = runtimeSetup();
    report('starting');
    report('running');
    expect(events).toEqual([
      {
        v: PROTOCOL_VERSION,
        type: 'session.runtime',
        sessionId: 's1',
        state: 'starting',
        capabilities: caps,
      },
      {
        v: PROTOCOL_VERSION,
        type: 'session.runtime',
        sessionId: 's1',
        state: 'running',
        capabilities: caps,
      },
    ]);
  });

  it('ignores runtime messages from a device that does not own the session', () => {
    const { control, events } = runtimeSetup();
    const other = fakeLink();
    control.attach('dev-b', other);
    events.length = 0;
    control.receive(other, agentState('s1', 'running'));
    control.receive(other, {
      v: PROTOCOL_VERSION,
      type: 'runtime.output',
      sessionId: 's1',
      kind: 'text',
      text: 'x',
    });
    expect(control.runtimeOf('s1').state).toBe('idle');
    expect(events).toEqual([]);
    // A link control does not know sends nothing either.
    control.receive(fakeLink(), agentState('s1', 'running'));
    expect(events).toEqual([]);
  });

  it('refuses prompts by state before anything is sent', async () => {
    const { control, link, report } = runtimeSetup();
    const ask = (id: string) => control.prompt('s1', 'dev-a', 'x', id);
    report('external');
    expect(await ask('a')).toEqual({ ok: false, error: 'external', promptId: 'a' });
    report('starting');
    expect(await ask('b')).toEqual({ ok: false, error: 'starting', promptId: 'b' });
    report('running');
    expect(await ask('c')).toEqual({ ok: false, error: 'busy', promptId: 'c' });
    report('waiting-permission');
    expect(await ask('d')).toEqual({ ok: false, error: 'busy', promptId: 'd' });
    expect(link.sent).toEqual([]);
    link.open = false;
    expect(await ask('e')).toEqual({ ok: false, error: 'owner-offline', promptId: 'e' });
  });

  it('forwards a prompt in idle, ready and failed, and resolves when the agent took it', async () => {
    const { control, link, report } = runtimeSetup();
    for (const [i, state] of ['idle', 'ready', 'failed'].entries()) {
      report(state);
      const pending = control.prompt('s1', 'dev-a', 'go', `p${i}`);
      expect(link.sent.at(-1)).toMatchObject({
        type: 'runtime.prompt',
        promptId: `p${i}`,
        text: 'go',
      });
      report(i === 0 ? 'starting' : 'running');
      expect(await pending).toEqual({ ok: true, promptId: `p${i}` });
    }
  });

  it('maps an agent refusal to its code, or to rejected with the reason', async () => {
    const { control, link } = runtimeSetup();
    const refuse = async (promptId: string, reason: string) => {
      const pending = control.prompt('s1', 'dev-a', 'x', promptId);
      control.receive(link, {
        v: PROTOCOL_VERSION,
        type: 'runtime.rejected',
        sessionId: 's1',
        promptId,
        reason,
      });
      return pending;
    };
    expect(await refuse('a', 'busy')).toEqual({ ok: false, error: 'busy', promptId: 'a' });
    expect(await refuse('b', 'session-not-on-this-device')).toEqual({
      ok: false,
      error: 'rejected',
      promptId: 'b',
      reason: 'session-not-on-this-device',
    });
  });

  it('answers a repeated promptId with the first result for 10 minutes', async () => {
    let now = 1_000_000;
    const { control, link, report } = runtimeSetup(() => now);
    const first = control.prompt('s1', 'dev-a', 'go', 'same');
    const again = control.prompt('s1', 'dev-a', 'go', 'same'); // while the first is in flight
    expect(link.sent).toHaveLength(1);
    report('running');
    expect(await first).toEqual({ ok: true, promptId: 'same' });
    expect(await again).toEqual({ ok: true, promptId: 'same' });

    report('ready');
    now += 9 * 60_000;
    expect(await control.prompt('s1', 'dev-a', 'go', 'same')).toEqual({
      ok: true,
      promptId: 'same',
    });
    expect(link.sent).toHaveLength(1);

    now += 2 * 60_000; // past 10 minutes
    const later = control.prompt('s1', 'dev-a', 'go', 'same');
    expect(link.sent).toHaveLength(2);
    report('running');
    expect(await later).toEqual({ ok: true, promptId: 'same' });
  });

  it('does not repeat a refusal: the same promptId can be sent again', async () => {
    const { control, link, report } = runtimeSetup();
    report('running');
    expect(await control.prompt('s1', 'dev-a', 'go', 'p')).toMatchObject({ error: 'busy' });
    report('ready');
    const pending = control.prompt('s1', 'dev-a', 'go', 'p');
    expect(link.sent).toHaveLength(1);
    report('running');
    expect(await pending).toEqual({ ok: true, promptId: 'p' });
  });

  it('repeats a timeout, since the prompt may have reached the agent', async () => {
    const { control, link } = runtimeSetup();
    expect(await control.prompt('s1', 'dev-a', 'go', 'p')).toMatchObject({ error: 'timeout' });
    expect(await control.prompt('s1', 'dev-a', 'go', 'p')).toMatchObject({ error: 'timeout' });
    expect(link.sent).toHaveLength(1);
  });

  it('deduplicates per session', async () => {
    const { store, control, link, report } = runtimeSetup();
    store.append({ sessionId: 's2', projectDir: 'p', filePath: '/y', deviceId: 'dev-a' }, ['{}']);
    const a = control.prompt('s1', 'dev-a', 'go', 'same');
    const b = control.prompt('s2', 'dev-a', 'go', 'same');
    expect(link.sent).toHaveLength(2);
    report('running');
    control.receive(link, agentState('s2', 'running'));
    expect(await a).toMatchObject({ ok: true });
    expect(await b).toMatchObject({ ok: true });
  });

  it('settles a prompt waiting for an agent that disconnects', async () => {
    const { control, link } = runtimeSetup();
    const pending = control.prompt('s1', 'dev-a', 'go', 'p');
    control.detach(link);
    expect(await pending).toEqual({ ok: false, error: 'owner-offline', promptId: 'p' });
  });

  describe('permission requests', () => {
    const request = (requestId: string, input: unknown = { command: 'ls' }) =>
      ({
        v: PROTOCOL_VERSION,
        type: 'runtime.permission',
        sessionId: 's1',
        requestId,
        tool: 'Bash',
        toolUseId: 'tu1',
        input,
      }) as ControlReply;

    it('publishes the request and forwards the first answer only', () => {
      const { control, link, events, report } = runtimeSetup();
      report('waiting-permission');
      control.receive(link, request('r1'));
      expect(events.at(-1)).toEqual({
        v: PROTOCOL_VERSION,
        type: 'session.permission',
        sessionId: 's1',
        requestId: 'r1',
        tool: 'Bash',
        toolUseId: 'tu1',
        input: { command: 'ls' },
      });
      expect(control.openPermissions('s1')).toHaveLength(1);

      expect(control.answerPermission('s1', 'dev-a', 'r1', 'allow')).toEqual({ ok: true });
      expect(link.sent.at(-1)).toEqual({
        v: PROTOCOL_VERSION,
        type: 'runtime.permission.answer',
        sessionId: 's1',
        requestId: 'r1',
        decision: 'allow',
      });
      const sent = link.sent.length;
      expect(control.answerPermission('s1', 'dev-a', 'r1', 'deny')).toEqual({
        ok: false,
        error: 'request-closed',
      });
      expect(link.sent).toHaveLength(sent);
      expect(control.openPermissions('s1')).toEqual([]);
    });

    it('closes a request nobody knows, one of another session, and one older than 10 minutes', () => {
      let now = 5_000_000;
      const { control, link, report } = runtimeSetup(() => now);
      const closed = { ok: false, error: 'request-closed' };
      expect(control.answerPermission('s1', 'dev-a', 'nope', 'allow')).toEqual(closed);
      report('waiting-permission');
      control.receive(link, request('r1'));
      expect(control.answerPermission('zz', 'dev-a', 'r1', 'allow')).toEqual(closed);
      control.receive(link, request('r2'));
      now += 10 * 60_000 + 1;
      expect(control.openPermissions('s1')).toEqual([]);
      expect(control.answerPermission('s1', 'dev-a', 'r2', 'allow')).toEqual(closed);
      expect(link.sent).toEqual([]);
    });

    it('drops the open requests when the turn is over', () => {
      const { control, link, report } = runtimeSetup();
      report('waiting-permission');
      control.receive(link, request('r1'));
      report('running'); // answered elsewhere: the turn goes on
      expect(control.openPermissions('s1')).toHaveLength(1);
      report('ready');
      expect(control.openPermissions('s1')).toEqual([]);
    });

    it('refuses to answer when the owner is offline', () => {
      const { control, link } = runtimeSetup();
      link.open = false;
      expect(control.answerPermission('s1', 'dev-a', 'r1', 'allow')).toEqual({
        ok: false,
        error: 'owner-offline',
      });
    });
  });

  it('forwards interrupt only while a turn runs, and stop always', () => {
    const { control, link, report } = runtimeSetup();
    report('ready');
    expect(control.interrupt('s1', 'dev-a')).toEqual({ ok: false, error: 'not-running' });
    report('running');
    expect(control.interrupt('s1', 'dev-a')).toEqual({ ok: true });
    report('waiting-permission');
    expect(control.interrupt('s1', 'dev-a')).toEqual({ ok: true });
    expect(link.sent.map((m) => m.type)).toEqual(['runtime.interrupt', 'runtime.interrupt']);
    expect(control.stop('s1', 'dev-a')).toEqual({ ok: true });
    expect(link.sent.at(-1)).toMatchObject({ type: 'runtime.stop', sessionId: 's1' });
    link.open = false;
    expect(control.stop('s1', 'dev-a')).toEqual({ ok: false, error: 'owner-offline' });
    expect(control.interrupt('s1', 'dev-a')).toEqual({ ok: false, error: 'owner-offline' });
  });

  it('forwards streamed output as session.output', () => {
    const { control, link, events } = runtimeSetup();
    control.receive(link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.output',
      sessionId: 's1',
      kind: 'tool-call',
      tool: 'Bash',
      toolUseId: 'tu1',
      input: { command: 'ls' },
    });
    expect(events).toEqual([
      {
        v: PROTOCOL_VERSION,
        type: 'session.output',
        sessionId: 's1',
        kind: 'tool-call',
        tool: 'Bash',
        toolUseId: 'tu1',
        input: { command: 'ls' },
      },
    ]);
  });

  it('keeps no output: only the state is remembered', () => {
    const { control, link, report } = runtimeSetup();
    control.receive(link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.output',
      sessionId: 's1',
      kind: 'text',
      text: 'hi',
    });
    report('running');
    expect(control.runtimeOf('s1')).toEqual({ state: 'running', capabilities: caps });
  });

  it('goes offline when the owner disconnects and rebuilds from the agent when it returns', () => {
    const { control, link, events, report } = runtimeSetup();
    report('running');
    control.receive(link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.permission',
      sessionId: 's1',
      requestId: 'r1',
      tool: 'Bash',
      input: {},
    });
    events.length = 0;

    control.detach(link);
    expect(kinds(events)).toEqual(['session.runtime:offline', 'device.presence']);
    expect(control.runtimeOf('s1').state).toBe('offline');
    expect(control.openPermissions('s1')).toEqual([]);

    // The new connection starts from nothing; the agent reports what it still runs.
    events.length = 0;
    const back = fakeLink();
    control.attach('dev-a', back);
    expect(kinds(events)).toEqual(['session.runtime:idle', 'device.presence']);
    expect(control.runtimeOf('s1').state).toBe('idle');
    control.receive(back, agentState('s1', 'running'));
    expect(control.runtimeOf('s1')).toEqual({ state: 'running', capabilities: caps });
    // The old link is no longer the device's connection.
    control.receive(link, agentState('s1', 'external'));
    expect(control.runtimeOf('s1').state).toBe('running');
  });

  it('keeps a replaced connection from marking the new one offline', () => {
    const { control, link, report } = runtimeSetup();
    report('ready');
    const next = fakeLink();
    control.attach('dev-a', next);
    control.detach(link); // the old socket closes after the new one attached
    expect(control.runtimeOf('s1').state).toBe('idle');
  });
});
