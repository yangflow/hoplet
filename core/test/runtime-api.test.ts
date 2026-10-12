import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION, parse, WsTicketResponseSchema } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { MAX_SUBSCRIPTIONS } from '../src/api/browser.ts';
import { createApi } from '../src/api/index.ts';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

const HOST = 'host-token-0123456789abcdef';
const CAPS = { permissions: true, interrupt: true, thinking: true, fast: false, models: [] };

const dirs: string[] = [];
const servers: Server[] = [];
const sockets: Peer[] = [];

afterEach(async () => {
  for (const p of sockets.splice(0)) p.ws.terminate();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

type Msg = Record<string, unknown>;

/** A WebSocket that keeps every message it receives. */
class Peer {
  readonly ws: WebSocket;
  readonly got: Msg[] = [];
  constructor(ws: WebSocket) {
    this.ws = ws;
    ws.on('message', (data) => this.got.push(JSON.parse(String(data)) as Msg));
  }
  send(msg: Msg): void {
    this.ws.send(JSON.stringify({ v: PROTOCOL_VERSION, ...msg }));
  }
  async waitFor(predicate: (m: Msg) => boolean, what: string): Promise<Msg> {
    for (let i = 0; i < 100; i++) {
      const found = this.got.find(predicate);
      if (found) return found;
      await new Promise((r) => setTimeout(r, 20));
    }
    throw new Error(
      `timed out waiting for ${what}; got ${JSON.stringify(this.got.map((m) => m.type))}`,
    );
  }
  ofType(type: string): Msg[] {
    return this.got.filter((m) => m.type === type);
  }
}

function open(url: string, headers: Record<string, string> = {}): Promise<Peer> {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(url, { headers });
    const peer = new Peer(ws);
    sockets.push(peer);
    ws.once('open', () => resolve(peer));
    ws.once('error', reject);
  });
}

async function boot() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-runtime-'));
  dirs.push(dir);
  const store = new Store(dir);
  for (const id of ['s1', 's2']) {
    store.append({ sessionId: id, projectDir: 'p', filePath: `/${id}`, deviceId: 'dev-a' }, [
      '{"type":"user"}',
    ]);
  }
  const control = createControl(store, { promptTimeoutMs: 600 });
  const server = createServer();
  attachIngest(server, store, control);
  createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control).attach(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  servers.push(server);
  const port = (server.address() as AddressInfo).port;
  const base = `http://127.0.0.1:${port}`;

  const post = async (p: string, body?: unknown) => {
    const res = await fetch(base + p, {
      method: 'POST',
      headers: { authorization: `Bearer ${HOST}`, 'content-type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    return { status: res.status, body: (await res.json()) as Msg };
  };
  const agent = async (deviceId: string) => {
    const { token } = store.mintDevice(deviceId, deviceId);
    const peer = await open(`ws://127.0.0.1:${port}/ws/agent`, {
      authorization: `Bearer ${token}`,
    });
    peer.send({ type: 'device.hello', deviceId, name: deviceId, version: 'test' });
    await peer.waitFor((m) => m.type === 'welcome', 'welcome');
    return peer;
  };
  const browser = async () => {
    const res = await fetch(`${base}/api/ws-tickets`, {
      method: 'POST',
      headers: { authorization: `Bearer ${HOST}` },
    });
    const { ticket } = parse(WsTicketResponseSchema, await res.json());
    return open(`ws://127.0.0.1:${port}/ws?ticket=${ticket}`);
  };
  return { store, control, post, agent, browser };
}

const state = (sessionId: string, st: string, extra: Msg = {}): Msg => ({
  type: 'runtime.state',
  sessionId,
  state: st,
  capabilities: CAPS,
  ...extra,
});

describe('POST /api/sessions/:id/prompt', () => {
  it('forwards the prompt and answers 202 when the agent reports the session running', async () => {
    const { post, agent } = await boot();
    const owner = await agent('dev-a');
    const pending = post('/api/sessions/s1/prompt', { text: 'hello', promptId: 'p1' });
    const forwarded = await owner.waitFor((m) => m.type === 'runtime.prompt', 'runtime.prompt');
    expect(forwarded).toMatchObject({ sessionId: 's1', promptId: 'p1', text: 'hello' });
    owner.send(state('s1', 'running'));
    expect(await pending).toEqual({ status: 202, body: { ok: true, promptId: 'p1' } });
  });

  it('refuses owner-offline, external, busy and starting with their codes', async () => {
    const { post, agent, browser } = await boot();
    const body = (id: string) => ({ text: 'x', promptId: id });
    expect(await post('/api/sessions/s1/prompt', body('a'))).toEqual({
      status: 409,
      body: { code: 'owner-offline' },
    });

    const owner = await agent('dev-a');
    const watcher = await browser();
    for (const [st, code] of [
      ['external', 'external'],
      ['starting', 'starting'],
      ['running', 'busy'],
      ['waiting-permission', 'busy'],
    ] as const) {
      owner.send(state('s1', st));
      await watcher.waitFor((m) => m.type === 'session.runtime' && m.state === st, st);
      const res = await post('/api/sessions/s1/prompt', body(`p-${st}`));
      expect(res, st).toEqual({ status: 409, body: { code, promptId: `p-${st}` } });
    }
    expect(owner.ofType('runtime.prompt')).toEqual([]);
    expect((await post('/api/sessions/zz/prompt', body('b'))).status).toBe(404);
  });

  it('maps an agent refusal and repeats the first result for a repeated promptId', async () => {
    const { post, agent } = await boot();
    const owner = await agent('dev-a');
    const refused = post('/api/sessions/s1/prompt', { text: 'x', promptId: 'r1' });
    await owner.waitFor((m) => m.type === 'runtime.prompt', 'runtime.prompt');
    owner.send({ type: 'runtime.rejected', sessionId: 's1', promptId: 'r1', reason: 'busy' });
    expect(await refused).toEqual({ status: 409, body: { code: 'busy', promptId: 'r1' } });

    const first = post('/api/sessions/s2/prompt', { text: 'x', promptId: 'same' });
    await owner.waitFor((m) => m.type === 'runtime.prompt' && m.promptId === 'same', 'prompt');
    owner.send(state('s2', 'starting'));
    expect(await first).toEqual({ status: 202, body: { ok: true, promptId: 'same' } });
    owner.send(state('s2', 'ready'));
    await new Promise((r) => setTimeout(r, 50));
    expect(await post('/api/sessions/s2/prompt', { text: 'x', promptId: 'same' })).toEqual({
      status: 202,
      body: { ok: true, promptId: 'same' },
    });
    expect(owner.ofType('runtime.prompt').filter((m) => m.promptId === 'same')).toHaveLength(1);
  });

  it('needs a promptId', async () => {
    const { post } = await boot();
    expect((await post('/api/sessions/s1/prompt', { text: 'x' })).body).toEqual({
      code: 'invalid-request',
    });
  });
});

describe('permission, interrupt and stop', () => {
  it('routes the first answer to the owner and refuses the second', async () => {
    const { post, agent, browser } = await boot();
    const owner = await agent('dev-a');
    const watcher = await browser();
    owner.send(state('s1', 'waiting-permission'));
    owner.send({
      type: 'runtime.permission',
      sessionId: 's1',
      requestId: 'r1',
      tool: 'Bash',
      input: { command: 'ls' },
    });
    // Every browser sees the request, subscribed or not.
    expect(
      await watcher.waitFor((m) => m.type === 'session.permission', 'permission'),
    ).toMatchObject({
      sessionId: 's1',
      requestId: 'r1',
      tool: 'Bash',
      input: { command: 'ls' },
    });

    const answers = await Promise.all([
      post('/api/sessions/s1/permission', { requestId: 'r1', decision: 'allow' }),
      post('/api/sessions/s1/permission', { requestId: 'r1', decision: 'deny' }),
    ]);
    expect(answers.map((a) => a.status).sort()).toEqual([200, 409]);
    expect(answers.find((a) => a.status === 409)?.body).toEqual({ code: 'request-closed' });
    const forwarded = await owner.waitFor((m) => m.type === 'runtime.permission.answer', 'answer');
    expect(forwarded).toMatchObject({ sessionId: 's1', requestId: 'r1' });
    expect(owner.ofType('runtime.permission.answer')).toHaveLength(1);
    // The decision forwarded is the one of the request that won.
    expect(forwarded.decision).toBe(answers[0]?.status === 200 ? 'allow' : 'deny');

    expect(
      (await post('/api/sessions/s1/permission', { requestId: 'r1', decision: 'maybe' })).body,
    ).toEqual({ code: 'invalid-request' });
  });

  it('forwards interrupt while a turn runs and refuses it otherwise', async () => {
    const { post, agent, browser } = await boot();
    expect((await post('/api/sessions/s1/interrupt')).body).toEqual({ code: 'owner-offline' });
    const owner = await agent('dev-a');
    const watcher = await browser();
    owner.send(state('s1', 'ready'));
    await watcher.waitFor((m) => m.state === 'ready', 'ready');
    expect(await post('/api/sessions/s1/interrupt')).toEqual({
      status: 409,
      body: { code: 'not-running' },
    });
    owner.send(state('s1', 'running'));
    await watcher.waitFor((m) => m.state === 'running', 'running');
    expect(await post('/api/sessions/s1/interrupt')).toEqual({ status: 200, body: { ok: true } });
    await owner.waitFor((m) => m.type === 'runtime.interrupt', 'runtime.interrupt');
  });

  it('forwards stop in any state', async () => {
    const { post, agent } = await boot();
    expect((await post('/api/sessions/s1/stop')).body).toEqual({ code: 'owner-offline' });
    const owner = await agent('dev-a');
    expect(await post('/api/sessions/s1/stop')).toEqual({ status: 200, body: { ok: true } });
    expect(await owner.waitFor((m) => m.type === 'runtime.stop', 'runtime.stop')).toMatchObject({
      sessionId: 's1',
    });
    expect((await post('/api/sessions/zz/stop')).status).toBe(404);
  });
});

describe('browser subscriptions', () => {
  it('sends output only to subscribers and runtime state to every browser', async () => {
    const { agent, browser } = await boot();
    const owner = await agent('dev-a');
    const subscribed = await browser();
    const other = await browser();
    subscribed.send({ type: 'subscribe', sessionId: 's1' });
    await subscribed.waitFor((m) => m.type === 'session.runtime', 'snapshot');

    owner.send({ type: 'runtime.output', sessionId: 's1', kind: 'text', text: 'hel' });
    owner.send({ type: 'runtime.output', sessionId: 's2', kind: 'text', text: 'other' });
    owner.send(state('s1', 'running'));
    await other.waitFor((m) => m.type === 'session.runtime' && m.state === 'running', 'state');
    await subscribed.waitFor((m) => m.type === 'session.output', 'output');
    expect(subscribed.ofType('session.output')).toEqual([
      { v: PROTOCOL_VERSION, type: 'session.output', sessionId: 's1', kind: 'text', text: 'hel' },
    ]);
    expect(other.ofType('session.output')).toEqual([]);

    subscribed.send({ type: 'unsubscribe', sessionId: 's1' });
    subscribed.send({ type: 'subscribe', sessionId: 's2' });
    await subscribed.waitFor((m) => m.type === 'session.runtime' && m.sessionId === 's2', 's2');
    owner.send({ type: 'runtime.output', sessionId: 's1', kind: 'text', text: 'lo' });
    owner.send({ type: 'runtime.output', sessionId: 's2', kind: 'text', text: 'two' });
    await subscribed.waitFor((m) => m.type === 'session.output' && m.text === 'two', 'two');
    expect(subscribed.ofType('session.output').map((m) => m.text)).toEqual(['hel', 'two']);
  });

  it('answers a subscription with the current state and the open permission requests', async () => {
    const { agent, browser } = await boot();
    const owner = await agent('dev-a');
    const early = await browser();
    owner.send(state('s1', 'waiting-permission'));
    owner.send({
      type: 'runtime.permission',
      sessionId: 's1',
      requestId: 'r1',
      tool: 'Edit',
      input: {},
    });
    expect(await early.waitFor((m) => m.type === 'session.permission', 'permission')).toMatchObject(
      { requestId: 'r1', tool: 'Edit', toolKind: 'edit' },
    );

    const late = await browser();
    late.send({ type: 'subscribe', sessionId: 's1' });
    expect(await late.waitFor((m) => m.type === 'session.runtime', 'state')).toMatchObject({
      sessionId: 's1',
      state: 'waiting-permission',
      capabilities: CAPS,
    });
    expect(await late.waitFor((m) => m.type === 'session.permission', 'request')).toMatchObject({
      requestId: 'r1',
      tool: 'Edit',
      toolKind: 'edit',
    });
  });

  it('classifies streamed tool calls by the session provider and passes other output as it is', async () => {
    const { agent, browser } = await boot();
    const owner = await agent('dev-a');
    const web = await browser();
    web.send({ type: 'subscribe', sessionId: 's1' });
    await web.waitFor((m) => m.type === 'session.runtime', 'snapshot');

    const call = (tool: string, toolUseId: string): Msg => ({
      type: 'runtime.output',
      sessionId: 's1',
      kind: 'tool-call',
      tool,
      toolUseId,
      input: {},
    });
    owner.send(call('Bash', 'a'));
    owner.send(call('mcp__github__list_prs', 'b'));
    owner.send(call('TodoWrite', 'c'));
    owner.send({ type: 'runtime.output', sessionId: 's1', kind: 'tool-result', toolUseId: 'a' });
    await web.waitFor((m) => m.type === 'session.output' && m.kind === 'tool-result', 'result');
    expect(web.ofType('session.output')).toEqual([
      { v: PROTOCOL_VERSION, ...call('Bash', 'a'), type: 'session.output', toolKind: 'run' },
      {
        v: PROTOCOL_VERSION,
        ...call('mcp__github__list_prs', 'b'),
        type: 'session.output',
        toolKind: 'call',
        mcp: { server: 'github', tool: 'list_prs' },
      },
      { v: PROTOCOL_VERSION, ...call('TodoWrite', 'c'), type: 'session.output', toolKind: 'other' },
      {
        v: PROTOCOL_VERSION,
        type: 'session.output',
        sessionId: 's1',
        kind: 'tool-result',
        toolUseId: 'a',
      },
    ]);
  });

  it('reports offline for a session whose owner is not connected', async () => {
    const { browser } = await boot();
    const watcher = await browser();
    watcher.send({ type: 'subscribe', sessionId: 's1' });
    expect(await watcher.waitFor((m) => m.type === 'session.runtime', 'state')).toMatchObject({
      sessionId: 's1',
      state: 'offline',
    });
  });

  it('refuses malformed messages and too many subscriptions without closing', async () => {
    const { browser } = await boot();
    const watcher = await browser();
    watcher.ws.send('not json');
    watcher.send({ type: 'subscribe' });
    watcher.send({ type: 'subscribe', sessionId: '../x' });
    watcher.ws.send(
      JSON.stringify({ v: PROTOCOL_VERSION + 1, type: 'subscribe', sessionId: 's1' }),
    );
    await watcher.waitFor((m) => m.code === 'protocol-version-mismatch', 'version error');
    expect(watcher.ofType('error').map((m) => m.code)).toEqual([
      'invalid-json',
      'invalid-message',
      'invalid-message',
      'protocol-version-mismatch',
    ]);
    for (let i = 0; i <= MAX_SUBSCRIPTIONS; i++) {
      watcher.send({ type: 'subscribe', sessionId: `x${i}` });
    }
    await watcher.waitFor((m) => m.code === 'too-large', 'too many');
    expect(watcher.ws.readyState).toBe(WebSocket.OPEN);
  });
});

describe('reconnect', () => {
  it('marks the sessions offline, then rebuilds their state from the agent', async () => {
    const { agent, browser, control } = await boot();
    const owner = await agent('dev-a');
    const watcher = await browser();
    owner.send(state('s1', 'running'));
    await watcher.waitFor((m) => m.type === 'session.runtime' && m.state === 'running', 'running');

    owner.ws.close();
    await watcher.waitFor((m) => m.type === 'session.runtime' && m.state === 'offline', 'offline');
    expect(control.runtimeOf('s1').state).toBe('offline');

    const again = await agent('dev-a');
    await watcher.waitFor(
      (m) => m.type === 'session.runtime' && m.state === 'idle' && watcher.got.indexOf(m) > 1,
      'reset',
    );
    again.send(state('s1', 'ready'));
    await watcher.waitFor(
      (m) => m.type === 'session.runtime' && m.state === 'ready',
      'rebuilt from the agent',
    );
    expect(control.runtimeOf('s1').state).toBe('ready');
  });
});
