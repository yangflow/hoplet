import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  DEFAULT_SESSION_MCP_SETTINGS,
  PROTOCOL_VERSION,
  type SessionMcpSettings,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createApi } from '../src/api/index.ts';
import { createControl, type SessionLimits } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

// biome-ignore lint/suspicious/noExplicitAny: parsed wire JSON, read by field name
type Json = Record<string, any>;

const HOST = 'host-token-0123456789abcdef';

const dirs: string[] = [];
const servers: Server[] = [];
const sockets: WebSocket[] = [];

afterEach(async () => {
  for (const ws of sockets.splice(0)) ws.terminate();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

/** Sessions a1..a5 and a6: machine A (a6 in another project); b1: machine B. */
const SESSIONS = [
  { id: 'a1', device: 'dev-a', cwd: '/work/a' },
  { id: 'a2', device: 'dev-a', cwd: '/work/a' },
  { id: 'a3', device: 'dev-a', cwd: '/work/a' },
  { id: 'a4', device: 'dev-a', cwd: '/work/a' },
  { id: 'a5', device: 'dev-a', cwd: '/work/a' },
  { id: 'a6', device: 'dev-a', cwd: '/work/b' },
  { id: 'b1', device: 'dev-b', cwd: '/other/c' },
];

const modeOption = (current: string) => ({
  type: 'select',
  id: 'mode',
  name: 'Permission mode',
  category: 'mode',
  currentValue: current,
  options: [
    { value: 'plan', name: 'Plan' },
    { value: 'default', name: 'Manual' },
    { value: 'acceptEdits', name: 'Accept edits' },
    { value: 'bypassPermissions', name: 'Bypass' },
  ],
});
const capabilities = (mode: string) => ({
  permissions: true,
  interrupt: true,
  config: [modeOption(mode)],
});

async function boot(limits: Partial<SessionLimits> = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-session-mcp-'));
  dirs.push(dir);
  const store = new Store(dir);
  for (const s of SESSIONS) {
    store.append(
      {
        sessionId: s.id,
        provider: 'claude',
        projectDir: 'p',
        filePath: `/x/${s.id}`,
        deviceId: s.device,
      },
      [
        JSON.stringify({
          type: 'user',
          cwd: s.cwd,
          message: { role: 'user', content: 'hello' },
        }),
      ],
    );
  }
  const control = createControl(store, { promptTimeoutMs: 600, sessionLimits: limits });
  const server = createServer();
  attachIngest(server, store, control);
  createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control).attach(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  servers.push(server);
  const port = (server.address() as AddressInfo).port;

  const rest = async (method: string, p: string, token: string, body?: unknown) => {
    const res = await fetch(`http://127.0.0.1:${port}${p}`, {
      method,
      headers: {
        authorization: `Bearer ${token}`,
        ...(body === undefined ? {} : { 'content-type': 'application/json' }),
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    const text = await res.text();
    return {
      status: res.status,
      headers: res.headers,
      body: (text ? JSON.parse(text) : null) as Json | null,
    };
  };

  // One agent for machine A; it answers every prompt as accepted.
  const device = store.mintDevice('dev-a', 'dev-a');
  const agent = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
    headers: { authorization: `Bearer ${device.token}` },
  });
  sockets.push(agent);
  const send = (msg: Json) => agent.send(JSON.stringify({ v: PROTOCOL_VERSION, ...msg }));
  const prompts: Json[] = [];
  const modes = new Map<string, string>();
  const report = (sessionId: string, state: string) =>
    send({
      type: 'runtime.state',
      sessionId,
      state,
      capabilities: capabilities(modes.get(sessionId) ?? 'default'),
    });
  agent.on('message', (data) => {
    const msg = JSON.parse(String(data)) as Json;
    if (msg.type === 'runtime.prompt') {
      prompts.push(msg);
      report(msg.sessionId, 'starting');
    }
  });
  await new Promise<void>((resolve, reject) => {
    agent.once('error', reject);
    agent.once('message', () => resolve());
    agent.once('open', () =>
      send({ type: 'device.hello', deviceId: 'dev-a', name: 'dev-a', version: 'test' }),
    );
  });
  /** Reports each session ready in its mode. */
  const ready = async (mode: Record<string, string> = {}) => {
    for (const s of SESSIONS.filter((x) => x.device === 'dev-a')) {
      modes.set(s.id, mode[s.id] ?? 'default');
      report(s.id, 'ready');
    }
    for (let i = 0; i < 100; i++) {
      if (
        SESSIONS.filter((x) => x.device === 'dev-a').every(
          (s) => control.runtimeOf(s.id).state === 'ready',
        )
      ) {
        return;
      }
      await new Promise((r) => setTimeout(r, 20));
    }
    throw new Error('sessions did not become ready');
  };

  const settings = async (id: string, patch: Partial<SessionMcpSettings>) => {
    const res = await rest('PUT', `/api/sessions/${id}/mcp`, HOST, {
      settings: { ...DEFAULT_SESSION_MCP_SETTINGS, ...patch },
    });
    expect(res.status).toBe(200);
  };
  const tokenOf = (id: string): string => {
    const minted = control.sessionMcp.mintToken(id);
    if (!minted) throw new Error('no token');
    return minted.secret;
  };
  const audit = () => store.listAudit({}, { order: 'asc', limit: 500 }).events;
  const prompt = (token: string, target: string, promptId: string) =>
    rest('POST', `/api/sessions/${target}/prompt`, token, { text: 'hi', promptId });
  const settle = () => new Promise((r) => setTimeout(r, 60));
  return { store, control, rest, ready, settings, tokenOf, audit, prompt, prompts, settle };
}

describe('the session token', () => {
  it('names the session, lists nowhere, and stops working when revoked', async () => {
    const b = await boot();
    const secret = b.tokenOf('a1');
    expect((await b.rest('GET', '/api/sessions', secret)).status).toBe(200);
    expect((await b.rest('GET', '/api/health', secret)).status).toBe(403);
    expect((await b.rest('GET', '/api/tokens', HOST)).body).toMatchObject({ tokens: [] });
    b.control.sessionMcp.revokeTokens('a1');
    expect((await b.rest('GET', '/api/sessions', secret)).status).toBe(401);
  });

  it('is not issued for a session that is not known', async () => {
    const b = await boot();
    expect(b.control.sessionMcp.mintToken('nope')).toBeNull();
  });

  it('expires within a day', async () => {
    const b = await boot();
    const minted = b.control.sessionMcp.mintToken('a1');
    expect((minted?.expiresAt ?? 0) - Date.now()).toBeLessThanOrEqual(24 * 3_600_000);
  });

  it('may call only the routes marked for sessions', async () => {
    const b = await boot();
    const secret = b.tokenOf('a1');
    for (const [method, p] of [
      ['GET', '/api/tokens'],
      ['GET', '/api/sessions/a1/mcp'],
      ['PUT', '/api/sessions/a1/mcp'],
      ['GET', '/api/mcp/defaults'],
      ['POST', '/api/ws-tickets'],
      ['GET', '/api/search?q=x'],
      ['GET', '/api/sessions/a1/queue'],
    ] as const) {
      expect((await b.rest(method, p, secret, method === 'PUT' ? {} : undefined)).status, p).toBe(
        403,
      );
    }
    const denied = b.audit().filter((e) => e.type === 'token.denied');
    expect(denied.length).toBeGreaterThan(0);
    expect(denied[0]).toMatchObject({ actorKind: 'session', actorId: 'a1', sessionId: 'a1' });
  });
});

describe('the settings', () => {
  it('start read only on the session own project', async () => {
    const b = await boot();
    const res = await b.rest('GET', '/api/sessions/a1/mcp', HOST);
    expect(res.body).toEqual({ sessionId: 'a1', settings: DEFAULT_SESSION_MCP_SETTINGS });
    expect(DEFAULT_SESSION_MCP_SETTINGS).toEqual({
      send: false,
      create: false,
      requestHandoff: false,
      visibility: 'project',
    });
  });

  it('are changed by the host alone and stored whole', async () => {
    const b = await boot();
    await b.settings('a1', { create: true, visibility: 'all' });
    const res = await b.rest('GET', '/api/sessions/a1/mcp', HOST);
    // `create` implies `send`.
    expect(res.body?.settings).toEqual({
      send: true,
      create: true,
      requestHandoff: false,
      visibility: 'all',
    });
    expect((await b.rest('GET', '/api/sessions/zz/mcp', HOST)).status).toBe(404);
    expect(
      (await b.rest('PUT', '/api/sessions/a1/mcp', HOST, { settings: { send: true } })).status,
    ).toBe(400);
    const changed = b.audit().filter((e) => e.type === 'config.changed');
    expect(changed.at(-1)).toMatchObject({ actorKind: 'operator', sessionId: 'a1' });
  });

  it('cannot be asked for through POST /api/tokens', async () => {
    const b = await boot();
    const res = await b.rest('POST', '/api/tokens', HOST, {
      name: 'x',
      grant: { capabilities: ['read', 'request-handoff'] },
      expiresInDays: 7,
    });
    expect(res.status).toBe(400);
  });

  it('take the project default, else the global default, for sessions that start later', async () => {
    const b = await boot();
    const put = (body: unknown) => b.rest('PUT', '/api/mcp/defaults', HOST, body);
    const global = { send: true, create: false, requestHandoff: false, visibility: 'machine' };
    const project = { send: true, create: true, requestHandoff: true, visibility: 'project' };
    expect((await put({ settings: global })).status).toBe(200);
    expect((await put({ deviceId: 'dev-a', path: '/work/a', settings: project })).status).toBe(200);
    expect((await put({ deviceId: 'dev-a', settings: project })).status).toBe(400);

    const defaults = await b.rest('GET', '/api/mcp/defaults', HOST);
    expect(defaults.body).toEqual({
      global,
      projects: [{ deviceId: 'dev-a', path: '/work/a', settings: project }],
    });
    // a1 is in /work/a, a6 in /work/b, b1 elsewhere.
    expect((await b.rest('GET', '/api/sessions/a1/mcp', HOST)).body?.settings).toEqual(project);
    expect((await b.rest('GET', '/api/sessions/a6/mcp', HOST)).body?.settings).toEqual(global);
    // A session that has settings keeps them when the default changes.
    expect((await put({ settings: DEFAULT_SESSION_MCP_SETTINGS })).status).toBe(200);
    expect((await b.rest('GET', '/api/sessions/a6/mcp', HOST)).body?.settings).toEqual(global);
    expect(
      (await b.rest('DELETE', '/api/mcp/defaults?deviceId=dev-a&path=%2Fwork%2Fa', HOST)).status,
    ).toBe(200);
    expect(
      (await b.rest('DELETE', '/api/mcp/defaults?deviceId=dev-a&path=%2Fwork%2Fa', HOST)).status,
    ).toBe(404);
  });
});

describe('what a session sees', () => {
  const ids = (body: Json | null): string[] =>
    ((body?.sessions ?? []) as { sessionId: string }[]).map((s) => s.sessionId).sort();

  it('is its project, its machine or all, and itself marked', async () => {
    const b = await boot();
    const secret = b.tokenOf('a1');
    const list = await b.rest('GET', '/api/sessions', secret);
    expect(ids(list.body)).toEqual(['a1', 'a2', 'a3', 'a4', 'a5']);
    const self = ((list.body?.sessions ?? []) as Json[]).filter((s) => s.self === true);
    expect(self.map((s) => s.sessionId)).toEqual(['a1']);

    expect((await b.rest('GET', '/api/sessions/a6', secret)).status).toBe(404);
    expect((await b.rest('GET', '/api/sessions/a2', secret)).status).toBe(200);
    expect((await b.rest('GET', '/api/sessions/a1', secret)).body?.self).toBe(true);

    // The grant is read at each request: widening the settings widens the same token.
    await b.settings('a1', { visibility: 'machine' });
    expect(ids((await b.rest('GET', '/api/sessions', secret)).body)).toEqual([
      'a1',
      'a2',
      'a3',
      'a4',
      'a5',
      'a6',
    ]);
    await b.settings('a1', { visibility: 'all' });
    expect(ids((await b.rest('GET', '/api/sessions', secret)).body)).toContain('b1');
    const devices = await b.rest('GET', '/api/devices', secret);
    expect(devices.status).toBe(200);
  });

  it('reads the lines of what it sees and nothing else', async () => {
    const b = await boot();
    const secret = b.tokenOf('a1');
    expect((await b.rest('GET', '/api/sessions/a2/lines', secret)).status).toBe(200);
    expect((await b.rest('GET', '/api/sessions/b1/lines', secret)).status).toBe(404);
  });
});

describe('prompting another session', () => {
  it('needs the send ability, looked up live', async () => {
    const b = await boot();
    await b.ready();
    const secret = b.tokenOf('a1');
    expect((await b.prompt(secret, 'a2', 'p1')).status).toBe(403);
    await b.settings('a1', { send: true });
    expect((await b.prompt(secret, 'a2', 'p2')).status).toBe(202);
    await b.settings('a1', { send: false });
    expect((await b.prompt(secret, 'a3', 'p3')).status).toBe(403);
  });

  it('is refused to itself, with the code self', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { send: true });
    const res = await b.prompt(b.tokenOf('a1'), 'a1', 'p1');
    expect(res.status).toBe(400);
    expect(res.body?.code).toBe('self');
    const stop = await b.rest('POST', '/api/sessions/a1/interrupt', b.tokenOf('a1'));
    expect(stop.status).toBe(400);
    expect(stop.body?.code).toBe('self');
  });

  it('is refused for a session that is out of scope as nonexistent', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { send: true });
    expect((await b.prompt(b.tokenOf('a1'), 'a6', 'p1')).body?.code).toBe('session-not-found');
  });

  it('never goes to a session in a more permissive mode', async () => {
    const b = await boot();
    await b.ready({ a1: 'acceptEdits', a2: 'bypassPermissions', a3: 'plan', a4: 'acceptEdits' });
    await b.settings('a1', { send: true });
    const secret = b.tokenOf('a1');
    const more = await b.prompt(secret, 'a2', 'p1');
    expect(more.status).toBe(403);
    expect(more.body).toMatchObject({ code: 'forbidden', reason: 'target-mode-more-permissive' });
    expect((await b.prompt(secret, 'a3', 'p2')).status).toBe(202);
    expect((await b.prompt(secret, 'a4', 'p3')).status).toBe(202);
    expect(b.prompts.map((p) => p.sessionId).sort()).toEqual(['a3', 'a4']);
    const interrupt = await b.rest('POST', '/api/sessions/a2/interrupt', secret);
    expect(interrupt.body).toMatchObject({ code: 'forbidden' });
  });

  it('is refused when a mode is unknown', async () => {
    const b = await boot();
    await b.settings('a1', { send: true });
    const res = await b.prompt(b.tokenOf('a1'), 'a2', 'p1');
    expect(res.status).toBe(403);
    expect(res.body?.code).toBe('forbidden');
  });

  it('is recorded with the session as actor and the chain behind it', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { send: true });
    await b.settings('a2', { send: true });
    expect((await b.prompt(b.tokenOf('a1'), 'a2', 'p1')).status).toBe(202);
    b.control.sessionMcp.revokeTokens('a1');
    const sent = b.audit().filter((e) => e.type === 'prompt.sent');
    expect(sent.at(-1)).toMatchObject({ actorKind: 'session', actorId: 'a1', sessionId: 'a2' });
    expect(sent.at(-1)?.summary.source).toEqual({
      kind: 'session',
      sessionId: 'a1',
      chain: ['a1'],
    });
    expect(JSON.stringify(sent)).not.toContain('"hi"');
    expect(b.store.sessionChain('a2')).toEqual(['a1']);
    // A prompt from the host starts a new chain.
    await b.settle();
    await b.ready();
    expect((await b.prompt(HOST, 'a2', 'p2')).status).toBe(202);
    expect(b.store.sessionChain('a2')).toEqual([]);
  });
});

describe('loops', () => {
  async function chainTo(
    b: Awaited<ReturnType<typeof boot>>,
    from: string,
    to: string,
    id: string,
  ) {
    await b.ready();
    return b.prompt(b.tokenOf(from), to, id);
  }

  it('refuses a prompt back to a session already in the chain', async () => {
    const b = await boot();
    for (const id of ['a1', 'a2']) await b.settings(id, { send: true });
    expect((await chainTo(b, 'a1', 'a2', 'p1')).status).toBe(202);
    const back = await chainTo(b, 'a2', 'a1', 'p2');
    expect(back.status).toBe(409);
    expect(back.body?.code).toBe('loop-limit');
  });

  it('refuses a chain longer than three sessions', async () => {
    const b = await boot();
    for (const id of ['a1', 'a2', 'a3', 'a4']) await b.settings(id, { send: true });
    expect((await chainTo(b, 'a1', 'a2', 'p1')).status).toBe(202);
    expect((await chainTo(b, 'a2', 'a3', 'p2')).status).toBe(202);
    expect((await chainTo(b, 'a3', 'a4', 'p3')).status).toBe(202);
    const longer = await chainTo(b, 'a4', 'a5', 'p4');
    expect(longer.status).toBe(409);
    expect(longer.body?.code).toBe('loop-limit');
  });
});

describe('limits', () => {
  it('allow three prompts a minute to one target, then answer 429 with Retry-After', async () => {
    const b = await boot({ unansweredPerTarget: 99 });
    await b.ready();
    await b.settings('a1', { send: true });
    const secret = b.tokenOf('a1');
    for (const id of ['p1', 'p2', 'p3']) {
      expect((await b.prompt(secret, 'a2', id)).status, id).toBe(202);
      await b.ready();
    }
    const res = await b.prompt(secret, 'a2', 'p4');
    expect(res.status).toBe(429);
    expect(res.body?.code).toBe('rate-limited');
    expect(Number(res.headers.get('retry-after'))).toBeGreaterThan(0);
    // Another target is not held back by it.
    expect((await b.prompt(secret, 'a3', 'p5')).status).toBe(202);
  });

  it('allow six prompts a minute in all', async () => {
    const b = await boot({ unansweredPerTarget: 99, perTargetPerMinute: 99 });
    await b.ready();
    await b.settings('a1', { send: true });
    const secret = b.tokenOf('a1');
    const targets = ['a2', 'a3', 'a4', 'a5', 'a2', 'a3'];
    for (const [i, target] of targets.entries()) {
      expect((await b.prompt(secret, target, `p${i}`)).status, `p${i}`).toBe(202);
      await b.ready();
    }
    const res = await b.prompt(secret, 'a4', 'p-more');
    expect(res.status).toBe(429);
    expect(Number(res.headers.get('retry-after'))).toBeGreaterThan(0);
  });

  it('allow two unanswered prompts to one target', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { send: true });
    const secret = b.tokenOf('a1');
    expect((await b.prompt(secret, 'a2', 'p1')).status).toBe(202);
    await b.ready();
    expect((await b.prompt(secret, 'a2', 'p2')).status).toBe(202);
    await b.ready();
    const third = await b.prompt(secret, 'a2', 'p3');
    expect(third.status).toBe(429);
    expect(third.body?.code).toBe('rate-limited');
  });

  it('do not count a prompt that was refused', async () => {
    const b = await boot({ perTargetPerMinute: 1 });
    await b.settings('a1', { send: true });
    // Not ready yet: the target has no mode, so the guard refuses before it counts.
    expect((await b.prompt(b.tokenOf('a1'), 'a2', 'p1')).status).toBe(403);
    await b.ready();
    expect((await b.prompt(b.tokenOf('a1'), 'a2', 'p2')).status).toBe(202);
  });
});
