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
    { value: 'bypassPermissions', name: 'Bypass', risk: 'high' },
  ],
});
const capabilities = (mode: string) => ({
  permissions: true,
  interrupt: true,
  config: [modeOption(mode)],
});

const CONFIGURATION = {
  id: 'claude',
  name: 'Claude Code',
  extends: 'claude',
  enabled: true,
  env: [],
  defaults: {},
};

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
  const starts: Json[] = [];
  const ruleSets: Json[] = [];
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
    if (msg.type === 'device.rules') ruleSets.push(msg);
    if (msg.type === 'device.projects.list') {
      send({
        type: 'device.projects',
        requestId: msg.requestId,
        candidates: [],
        projects: ['/work/a', '/work/b'].map((p) => ({ path: p, sessions: [] })),
      });
    }
    // A new session: the agent names it, then reports it ready in the mode it was asked for.
    if (msg.type === 'runtime.start') {
      starts.push(msg);
      const sessionId = `new-${starts.length}`;
      modes.set(sessionId, (msg.config as Json | undefined)?.mode ?? 'default');
      send({ type: 'runtime.started', requestId: msg.requestId, sessionId });
      report(sessionId, 'ready');
    }
  });
  await new Promise<void>((resolve, reject) => {
    agent.once('error', reject);
    agent.once('message', () => resolve());
    agent.once('open', () =>
      send({
        type: 'device.hello',
        deviceId: 'dev-a',
        name: 'dev-a',
        version: 'test',
        providers: [
          {
            provider: 'claude',
            configuration: CONFIGURATION,
            capabilities: capabilities('default'),
          },
        ],
      }),
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
  const start = (token: string, promptId: string, extra: Json = {}) =>
    rest('POST', '/api/sessions', token, {
      deviceId: 'dev-a',
      projectPath: '/work/a',
      provider: 'claude',
      text: 'build it',
      promptId,
      ...extra,
    });
  return {
    store,
    control,
    rest,
    ready,
    settings,
    tokenOf,
    audit,
    prompt,
    prompts,
    starts,
    ruleSets,
    start,
    settle,
  };
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
      autoApproveHandoff: false,
      autoApproveMachines: [],
      visibility: 'project',
      readConfig: true,
      writeConfig: false,
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
      autoApproveHandoff: false,
      autoApproveMachines: [],
      visibility: 'all',
      readConfig: true,
      writeConfig: false,
    });
    expect((await b.rest('GET', '/api/sessions/zz/mcp', HOST)).status).toBe(404);
    expect(
      (await b.rest('PUT', '/api/sessions/a1/mcp', HOST, { settings: { send: true } })).status,
    ).toBe(400);
    const changed = b.audit().filter((e) => e.type === 'config.changed');
    expect(changed.at(-1)).toMatchObject({ actorKind: 'operator', sessionId: 'a1' });
  });

  it('hold automatic hand-off approval only with a hand-off request and known machines', async () => {
    const b = await boot();
    const put = (id: string, patch: Partial<SessionMcpSettings>) =>
      b.rest('PUT', `/api/sessions/${id}/mcp`, HOST, {
        settings: { ...DEFAULT_SESSION_MCP_SETTINGS, ...patch },
      });
    expect((await put('a1', { autoApproveHandoff: true })).status).toBe(400);
    expect((await put('a1', { autoApproveMachines: ['dev-a'] })).status).toBe(400);
    expect((await put('a1', { requestHandoff: true, autoApproveMachines: ['nope'] })).status).toBe(
      400,
    );
    const ok = await put('a1', {
      requestHandoff: true,
      autoApproveHandoff: true,
      autoApproveMachines: ['dev-a', 'dev-a'],
    });
    expect(ok.status).toBe(200);
    expect(ok.body?.settings).toMatchObject({
      autoApproveHandoff: true,
      autoApproveMachines: ['dev-a'],
    });
    expect((await b.rest('GET', '/api/sessions/a1/mcp', HOST)).body?.settings).toMatchObject({
      autoApproveHandoff: true,
      autoApproveMachines: ['dev-a'],
    });
    const defaults = await b.rest('PUT', '/api/mcp/defaults', HOST, {
      settings: { ...DEFAULT_SESSION_MCP_SETTINGS, autoApproveHandoff: true },
    });
    expect(defaults.status).toBe(400);
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
    const global = {
      ...DEFAULT_SESSION_MCP_SETTINGS,
      send: true,
      visibility: 'machine',
    };
    const project = {
      ...DEFAULT_SESSION_MCP_SETTINGS,
      send: true,
      create: true,
      requestHandoff: true,
      autoApproveHandoff: true,
      autoApproveMachines: ['dev-a'],
    };
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

describe('queuing a prompt for another session', () => {
  const queue = (b: Awaited<ReturnType<typeof boot>>, token: string, target: string, id: string) =>
    b.rest('POST', `/api/sessions/${target}/queue`, token, { id, text: 'later' });

  it('passes the same checks as a prompt', async () => {
    const b = await boot();
    await b.ready({ a1: 'acceptEdits', a2: 'bypassPermissions', a3: 'plan' });
    const secret = b.tokenOf('a1');
    expect((await queue(b, secret, 'a3', 'q0')).status).toBe(403);
    await b.settings('a1', { send: true });
    const more = await queue(b, secret, 'a2', 'q1');
    expect(more.status).toBe(403);
    expect(more.body).toMatchObject({ code: 'forbidden', reason: 'target-mode-more-permissive' });
    const self = await queue(b, secret, 'a1', 'q2');
    expect(self.status).toBe(400);
    expect(self.body?.code).toBe('self');
    expect(b.store.queueOf('a2').items).toHaveLength(0);
    expect((await queue(b, secret, 'a3', 'q3')).status).toBe(201);
  });

  it('is limited like a prompt, and a refusal does not count', async () => {
    const b = await boot({ unansweredPerTarget: 99, perTargetPerMinute: 1 });
    await b.settings('a1', { send: true });
    // No mode known yet: refused before it counts.
    expect((await queue(b, b.tokenOf('a1'), 'a2', 'q1')).status).toBe(403);
    await b.ready();
    const secret = b.tokenOf('a1');
    expect((await queue(b, secret, 'a2', 'q2')).status).toBe(201);
    await b.ready();
    const again = await queue(b, secret, 'a2', 'q3');
    expect(again.status).toBe(429);
    expect(again.body?.code).toBe('rate-limited');
    expect(Number(again.headers.get('retry-after'))).toBeGreaterThan(0);
  });

  it('keeps the session as the author and sends its provenance with the prompt', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { send: true });
    expect((await queue(b, b.tokenOf('a1'), 'a2', 'q1')).status).toBe(201);
    await b.settle();
    const sent = b.prompts.at(-1);
    expect(sent).toMatchObject({ sessionId: 'a2', text: 'later' });
    expect(sent?.source).toMatchObject({ kind: 'session', sessionId: 'a1', chain: ['a1'] });
    expect(sent?.source?.title).toBe('hello');
    const audited = b.audit().filter((e) => e.type === 'prompt.sent');
    expect(audited.at(-1)).toMatchObject({ actorKind: 'session', actorId: 'a1', sessionId: 'a2' });
  });

  it('adds no provenance to a prompt from the host', async () => {
    const b = await boot();
    await b.ready();
    expect((await queue(b, HOST, 'a2', 'q1')).status).toBe(201);
    await b.settle();
    expect(b.prompts.at(-1)?.source).toBeUndefined();
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

describe('starting a session', () => {
  it('needs the create ability, looked up live', async () => {
    const b = await boot();
    await b.ready();
    const secret = b.tokenOf('a1');
    expect((await b.start(secret, 'p1')).status).toBe(403);
    await b.settings('a1', { send: true });
    expect((await b.start(secret, 'p2')).status).toBe(403);
    await b.settings('a1', { create: true });
    const res = await b.start(secret, 'p3');
    expect(res.status).toBe(200);
    expect(res.body).toMatchObject({ ok: true, sessionId: 'new-1', promptId: 'p3' });
    await b.settings('a1', { create: false });
    expect((await b.start(secret, 'p4')).status).toBe(403);
    expect(b.starts).toHaveLength(1);
  });

  it('records the chain, names the session in the first prompt and audits it', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { create: true });
    expect((await b.start(b.tokenOf('a1'), 'p1')).status).toBe(200);
    expect(b.starts[0]?.source).toMatchObject({ kind: 'session', sessionId: 'a1', chain: ['a1'] });
    expect(b.store.sessionChain('new-1')).toEqual(['a1']);
    const created = b.audit().filter((e) => e.type === 'session.created');
    expect(created.at(-1)).toMatchObject({
      actorKind: 'session',
      actorId: 'a1',
      sessionId: 'new-1',
    });
    expect(created.at(-1)?.summary.source).toEqual({
      kind: 'session',
      sessionId: 'a1',
      chain: ['a1'],
    });
    expect(JSON.stringify(created)).not.toContain('build it');
    // A start from the host has no source.
    expect((await b.start(HOST, 'p2')).status).toBe(200);
    expect(b.starts[1]?.source).toBeUndefined();
    expect(b.store.sessionChain('new-2')).toEqual([]);
  });

  it('reaches the project, the machine or all, as the settings say', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { create: true });
    const secret = b.tokenOf('a1');
    expect((await b.start(secret, 'p1', { projectPath: '/work/b' })).status).toBe(403);
    expect((await b.start(secret, 'p2', { deviceId: 'dev-b' })).status).toBe(403);
    await b.settings('a1', { create: true, visibility: 'machine' });
    expect((await b.start(secret, 'p3', { projectPath: '/work/b' })).status).toBe(200);
    expect(b.starts).toHaveLength(1);
  });

  it('never starts a session in a high-risk setting', async () => {
    const b = await boot();
    await b.ready({ a1: 'bypassPermissions' });
    await b.settings('a1', { create: true });
    const secret = b.tokenOf('a1');
    const res = await b.start(secret, 'p1', { config: { mode: 'bypassPermissions' } });
    expect(res.status).toBe(403);
    // Without a mode the session gets the highest one below it that is not high risk.
    expect((await b.start(secret, 'p2')).status).toBe(200);
    expect(b.starts[0]?.config).toEqual({ mode: 'acceptEdits' });
  });

  it('is never more permissive than the starting session, and fills the mode in', async () => {
    const b = await boot();
    await b.ready({ a1: 'plan', a2: 'acceptEdits' });
    await b.settings('a1', { create: true });
    await b.settings('a2', { create: true });
    const plan = b.tokenOf('a1');
    const more = await b.start(plan, 'p1', { config: { mode: 'acceptEdits' } });
    expect(more.status).toBe(403);
    expect(more.body).toMatchObject({ code: 'forbidden', reason: 'target-mode-more-permissive' });
    expect((await b.start(plan, 'p2', { config: { mode: 'unheard-of' } })).body?.reason).toBe(
      'target-mode-unknown',
    );
    expect((await b.start(plan, 'p3')).status).toBe(200);
    expect(b.starts[0]?.config).toEqual({ mode: 'plan' });
    expect((await b.start(b.tokenOf('a2'), 'p4', { config: { mode: 'default' } })).status).toBe(
      200,
    );
    expect(b.starts[1]?.config).toEqual({ mode: 'default' });
    expect((await b.start(b.tokenOf('a2'), 'p5')).status).toBe(200);
    expect(b.starts[2]?.config).toEqual({ mode: 'acceptEdits' });
  });

  it('is refused when the starting session mode is unknown', async () => {
    const b = await boot();
    await b.settings('a1', { create: true });
    const res = await b.start(b.tokenOf('a1'), 'p1');
    expect(res.status).toBe(403);
    expect(res.body?.reason).toBe('sender-mode-unknown');
    expect(b.starts).toHaveLength(0);
  });

  it('refuses a chain longer than three sessions with loop-limit', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { create: true });
    b.store.setSessionChain('a1', ['x', 'y', 'z']);
    const res = await b.start(b.tokenOf('a1'), 'p1');
    expect(res.status).toBe(409);
    expect(res.body?.code).toBe('loop-limit');
    // One link fewer is allowed, and the new session's chain is the source's plus one.
    b.store.setSessionChain('a1', ['x']);
    expect((await b.start(b.tokenOf('a1'), 'p2')).status).toBe(200);
    expect(b.store.sessionChain('new-1')).toEqual(['x', 'a1']);
  });

  it('allows the limit of starts an hour, then answers 429 with Retry-After', async () => {
    const b = await boot({ startsPerHour: 2 });
    await b.ready();
    await b.settings('a1', { create: true });
    const secret = b.tokenOf('a1');
    expect((await b.start(secret, 'p1')).status).toBe(200);
    // A start the core refuses does not count.
    expect((await b.start(secret, 'bad', { projectPath: '/elsewhere' })).status).toBe(403);
    expect((await b.start(secret, 'p2')).status).toBe(200);
    const limited = await b.start(secret, 'p3');
    expect(limited.status).toBe(429);
    expect(limited.body?.code).toBe('rate-limited');
    expect(Number(limited.headers.get('retry-after'))).toBeGreaterThan(0);
    expect(b.starts).toHaveLength(2);
  });

  it('does not count a start the device refused', async () => {
    const b = await boot({ startsPerHour: 1 });
    await b.ready();
    await b.settings('a1', { create: true, visibility: 'all' });
    const secret = b.tokenOf('a1');
    expect((await b.start(secret, 'p1', { projectPath: '/not/watched' })).status).toBe(400);
    expect((await b.start(secret, 'p2')).status).toBe(200);
  });

  it('allows a limited number of live sessions it started', async () => {
    const b = await boot({ liveChildren: 1 });
    await b.ready();
    await b.settings('a1', { create: true });
    const secret = b.tokenOf('a1');
    expect((await b.start(secret, 'p1')).status).toBe(200);
    await b.settle();
    const second = await b.start(secret, 'p2');
    expect(second.status).toBe(409);
    expect(second.body?.code).toBe('children-limit');
    expect(b.starts).toHaveLength(1);
  });
});

const usageLine = (id: string, input: number) =>
  JSON.stringify({
    type: 'assistant',
    cwd: '/work/a',
    timestamp: new Date().toISOString(),
    message: {
      id,
      model: 'claude-opus-5-5',
      role: 'assistant',
      content: [{ type: 'text', text: 'x' }],
      usage: {
        input_tokens: input,
        output_tokens: 0,
        cache_creation_input_tokens: 0,
        cache_read_input_tokens: 0,
      },
    },
  });
const meta = (id: string) => ({
  sessionId: id,
  provider: 'claude' as const,
  projectDir: 'p',
  filePath: `/x/${id}`,
  deviceId: 'dev-a',
});

describe('a tree of work', () => {
  it('refuses a start deeper than two levels with depth-limit, and audits it', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { create: true });
    b.store.recordChild('a1', ['x', 'y'], Date.now(), 30 * 60_000);
    const res = await b.start(b.tokenOf('a1'), 'p1');
    expect(res.status).toBe(409);
    expect(res.body?.code).toBe('depth-limit');
    expect(b.audit().find((e) => e.type === 'token.denied')).toMatchObject({
      actorKind: 'session',
      sessionId: 'a1',
    });
    expect(JSON.stringify(b.audit().filter((e) => e.type === 'token.denied'))).toContain(
      'depth-limit',
    );
  });

  it('allows a limited number of live sessions in one tree', async () => {
    const b = await boot({ liveTree: 2 });
    await b.ready();
    await b.settings('a1', { create: true });
    const secret = b.tokenOf('a1');
    expect((await b.start(secret, 'p1')).status).toBe(200);
    await b.settle();
    expect((await b.start(secret, 'p2')).status).toBe(200);
    await b.settle();
    const third = await b.start(secret, 'p3');
    expect(third.status).toBe(409);
    expect(third.body?.code).toBe('children-limit');
  });

  it('allows twelve starts an hour', async () => {
    const b = await boot({ liveChildren: 100, liveTree: 100 });
    await b.ready();
    await b.settings('a1', { create: true });
    const secret = b.tokenOf('a1');
    for (let i = 0; i < 12; i++) {
      expect((await b.start(secret, `p${i}`)).status, `start ${i}`).toBe(200);
    }
    expect((await b.start(secret, 'p12')).status).toBe(429);
  });

  it('refuses a start with budget-exceeded under a pause budget', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { create: true });
    b.store.append(meta('a5'), [usageLine('m1', 5000)]);
    b.store.createBudget(
      {
        name: 'Cap',
        scope: { kind: 'global', value: '' },
        period: 'day',
        unit: 'tokens',
        amount: 100,
        alertPercent: 80,
        onLimit: 'pause',
        utcOffset: 0,
      },
      Date.now(),
    );
    const res = await b.start(b.tokenOf('a1'), 'p1');
    expect(res.status).toBe(409);
    expect(res.body?.code).toBe('budget-exceeded');
    expect(res.body?.budget).toBeTruthy();
  });

  it('refuses a start once the tree has spent its tokens', async () => {
    const b = await boot({ treeTokens: 1000 });
    await b.ready();
    await b.settings('a1', { create: true });
    b.store.recordChild('a2', ['a1'], Date.now(), 30 * 60_000);
    b.store.append(meta('a2'), [usageLine('m1', 2000)]);
    const res = await b.start(b.tokenOf('a1'), 'p1');
    expect(res.status).toBe(409);
    expect(res.body?.code).toBe('budget-exceeded');
    expect(res.body?.budget).toBeUndefined();
  });

  it('stores the time limit of a child: 30 minutes, or what its parent asks up to two hours', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { create: true });
    const secret = b.tokenOf('a1');
    expect((await b.start(secret, 'p1')).status).toBe(200);
    expect((await b.start(secret, 'p2', { timeLimitMinutes: 90 })).status).toBe(200);
    expect(b.store.sessionChildren().get('new-1')?.timeLimitMs).toBe(30 * 60_000);
    expect(b.store.sessionChildren().get('new-2')?.timeLimitMs).toBe(90 * 60_000);
    expect((await b.start(secret, 'p3', { timeLimitMinutes: 121 })).status).toBe(400);
    const host = await b.start(HOST, 'p4', { timeLimitMinutes: 10 });
    expect(host.status).toBe(400);
    expect(host.body?.code).toBe('invalid-request');
  });

  it('shows parentId and childCount, and lists the descendants within the reach of the caller', async () => {
    const b = await boot();
    b.store.recordChild('a2', ['a1'], Date.now(), 30 * 60_000);
    b.store.recordChild('a3', ['a1', 'a2'], Date.now(), 30 * 60_000);
    b.store.recordChild('a6', ['a1'], Date.now(), 30 * 60_000);
    const one = await b.rest('GET', '/api/sessions/a1', HOST);
    expect(one.body).toMatchObject({ parentId: null, childCount: 2 });
    const list = await b.rest('GET', '/api/sessions', HOST);
    const by = (id: string) =>
      ((list.body?.sessions ?? []) as Json[]).find((x) => x.sessionId === id);
    expect(by('a3')).toMatchObject({ parentId: 'a2', childCount: 0 });
    expect(by('a2')).toMatchObject({ parentId: 'a1', childCount: 1 });

    const tree = await b.rest('GET', '/api/sessions/a1/children', HOST);
    expect(tree.body?.truncated).toBe(false);
    expect(((tree.body?.descendants ?? []) as Json[]).map((d) => [d.sessionId, d.depth])).toEqual([
      ['a2', 1],
      ['a3', 2],
      ['a6', 1],
    ]);
    // A session sees its own project only: a6 is in another one.
    await b.settings('a1', { send: true });
    const mine = await b.rest('GET', '/api/sessions/a1/children', b.tokenOf('a1'));
    expect(((mine.body?.descendants ?? []) as Json[]).map((d) => d.sessionId)).toEqual([
      'a2',
      'a3',
    ]);
    expect(mine.body?.truncated).toBe(true);
    expect((await b.rest('GET', '/api/sessions/zz/children', HOST)).status).toBe(404);
  });

  it('stops the live descendants with cascade, for the host alone', async () => {
    const b = await boot();
    await b.ready();
    b.store.recordChild('a2', ['a1'], Date.now(), 30 * 60_000);
    b.store.recordChild('a3', ['a1', 'a2'], Date.now(), 30 * 60_000);
    await b.settings('a1', { send: true });
    const refused = await b.rest('POST', '/api/sessions/a2/stop?cascade=true', b.tokenOf('a1'));
    expect(refused.status).toBe(403);
    const plain = await b.rest('POST', '/api/sessions/a2/stop', HOST);
    expect(plain.body).toEqual({ ok: true });
    const res = await b.rest('POST', '/api/sessions/a1/stop?cascade=true', HOST);
    expect(res.status).toBe(200);
    expect(res.body?.stopped).toEqual(expect.arrayContaining(['a3']));
    expect(
      b
        .audit()
        .filter((e) => e.type === 'session.stopped')
        .map((e) => e.sessionId),
    ).toEqual(expect.arrayContaining(['a1', 'a3']));
  });
});

describe('the tree watcher', () => {
  it('stops a child past its time limit once, and audits child.limit', async () => {
    const b = await boot();
    await b.ready();
    b.store.recordChild('a2', ['a1'], Date.now() - 3_600_000, 60_000);
    b.store.recordChild('a3', ['a1', 'a2'], Date.now(), 30 * 60_000);
    b.control.checkTrees();
    b.control.checkTrees();
    const events = b.audit().filter((e) => e.type === 'child.limit');
    // The child and what it started; a1 is the root and keeps running.
    expect(events.map((e) => e.sessionId).sort()).toEqual(['a2', 'a3']);
    expect(events[0]?.actorKind).toBe('core');
    expect(JSON.stringify(events[0]?.summary)).toContain('time');
  });

  it('leaves a child within its limit alone', async () => {
    const b = await boot();
    await b.ready();
    b.store.recordChild('a2', ['a1'], Date.now(), 60 * 60_000);
    b.control.checkTrees();
    expect(b.audit().filter((e) => e.type === 'child.limit')).toEqual([]);
  });

  it('stops the live sessions of a tree that spent its tokens', async () => {
    const b = await boot({ treeTokens: 1000 });
    await b.ready();
    b.store.recordChild('a2', ['a1'], Date.now(), 30 * 60_000);
    b.store.recordChild('a3', ['a1'], Date.now(), 30 * 60_000);
    b.store.append(meta('a2'), [usageLine('m1', 2000)]);
    b.control.checkTrees();
    const events = b.audit().filter((e) => e.type === 'child.limit');
    expect(events.map((e) => e.sessionId).sort()).toEqual(['a2', 'a3']);
    expect(JSON.stringify(events[0]?.summary)).toContain('tokens');
  });
});

describe('the relation of a child', () => {
  it('is not made by a prompt: a send_prompt to a peer leaves it without a parent', async () => {
    const b = await boot();
    await b.ready();
    // The peer a2 got a prompt of a1: its provenance names a1, but a1 did not start it.
    b.store.setSessionChain('a2', ['a1']);
    const peer = await b.rest('GET', '/api/sessions/a2', HOST);
    expect(peer.body).toMatchObject({ parentId: null, childCount: 0 });
    expect(((await b.rest('GET', '/api/sessions/a1', HOST)).body as Json).childCount).toBe(0);
    expect(
      ((await b.rest('GET', '/api/sessions/a1/children', HOST)).body?.descendants ?? []) as Json[],
    ).toEqual([]);
    // Stopping the sender with cascade leaves the peer running, and the watcher does not touch it.
    const res = await b.rest('POST', '/api/sessions/a1/stop?cascade=true', HOST);
    expect(res.body?.stopped).toEqual([]);
    b.control.checkTrees();
    expect(b.audit().filter((e) => e.type === 'child.limit')).toEqual([]);
  });

  it('does not count a prompted peer against the limits of the sender', async () => {
    const b = await boot({ liveChildren: 1, liveTree: 1 });
    await b.ready();
    await b.settings('a1', { create: true });
    b.store.setSessionChain('a2', ['a1']);
    b.store.setSessionChain('a3', ['a1']);
    expect((await b.start(b.tokenOf('a1'), 'p1')).status).toBe(200);
  });

  it('stays when a user prompts the child: it keeps its parent and its tree', async () => {
    const b = await boot();
    await b.ready();
    b.store.recordChild('a2', ['a1'], Date.now(), 30 * 60_000);
    b.store.recordChild('a3', ['a1', 'a2'], Date.now(), 30 * 60_000);
    // A prompt of a user removes the provenance of a2.
    b.store.setSessionChain('a2', []);
    expect(((await b.rest('GET', '/api/sessions/a2', HOST)).body as Json).parentId).toBe('a1');
    expect(((await b.rest('GET', '/api/sessions/a3', HOST)).body as Json).parentId).toBe('a2');
    const tree = await b.rest('GET', '/api/sessions/a1/children', HOST);
    expect(((tree.body?.descendants ?? []) as Json[]).map((d) => d.sessionId)).toEqual([
      'a2',
      'a3',
    ]);
  });

  it('takes the depth from the creation chain, not from the provenance', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { create: true });
    // A long provenance of a prompt means nothing for the depth: a1 was not started by a session.
    b.store.setSessionChain('a1', ['x']);
    expect((await b.start(b.tokenOf('a1'), 'p1')).status).toBe(200);
    expect(b.store.sessionChildren().get('new-1')?.chain).toEqual(['a1']);
    // The child of a child sits at depth two, which is allowed; the next level is not.
    b.store.recordChild('a2', ['r', 'a1'], Date.now(), 30 * 60_000);
    await b.settings('a2', { create: true });
    const deep = await b.start(b.tokenOf('a2'), 'p2');
    expect(deep.status).toBe(409);
    expect(deep.body?.code).toBe('depth-limit');
  });

  it('is forgotten with the session', async () => {
    const b = await boot();
    b.store.recordChild('a2', ['a1'], Date.now(), 30 * 60_000);
    b.store.forgetSessionMcp('a2');
    expect(b.store.sessionChildren().has('a2')).toBe(false);
  });
});

describe('the rules of a child (ADR 0029)', () => {
  const sessionRule = (sessionId: string, action: string, command = 'rm') => ({
    scope: 'session',
    sessionId,
    tool: 'run',
    match: { command },
    action,
  });
  const addRule = async (b: Awaited<ReturnType<typeof boot>>, body: Json): Promise<string> => {
    const res = await b.rest('POST', '/api/permission-rules', HOST, body);
    expect(res.status).toBe(201);
    return res.body?.id as string;
  };

  it('sends a new child the deny and ask rules of its parent with runtime.start, and no allow', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { create: true });
    const deny = await addRule(b, sessionRule('a1', 'deny'));
    const ask = await addRule(b, sessionRule('a1', 'ask', 'push'));
    await addRule(b, sessionRule('a1', 'allow', 'ls'));
    const off = await addRule(b, { ...sessionRule('a1', 'deny', 'dd'), enabled: false });
    await addRule(b, sessionRule('a3', 'deny', 'other'));
    expect((await b.start(b.tokenOf('a1'), 'p1')).status).toBe(200);
    const sent = b.starts[0]?.inheritedRules as Json[];
    expect(sent.map((r) => r.id).sort()).toEqual([deny, ask].sort());
    expect(sent.map((r) => r.id)).not.toContain(off);
    expect(sent.every((r) => r.action !== 'allow' && r.scope === 'session')).toBe(true);
  });

  it('sends nothing for a parent without rules', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { create: true });
    expect((await b.start(b.tokenOf('a1'), 'p1')).status).toBe(200);
    expect(b.starts[0]?.inheritedRules).toBeUndefined();
  });

  it('records the inherited rules and the device revision in the audit log', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { create: true });
    const deny = await addRule(b, sessionRule('a1', 'deny'));
    expect((await b.start(b.tokenOf('a1'), 'p1')).status).toBe(200);
    const created = b.audit().find((e) => e.type === 'session.created');
    const rules = ((created?.summary ?? {}) as Json).rules as Json;
    expect(rules.inherited).toEqual([deny]);
    expect(rules.revision).toBe(b.store.permissionRevision('dev-a'));
  });

  it('lists a child under its own ID in device.rules, with the ID of the parent rule', async () => {
    const b = await boot();
    await b.ready();
    await b.settings('a1', { create: true });
    const deny = await addRule(b, sessionRule('a1', 'deny'));
    await addRule(b, sessionRule('a1', 'allow', 'ls'));
    b.store.recordChild('a2', ['a1'], Date.now(), 30 * 60_000);
    b.store.bumpPermissionRevision('dev-a');
    b.control.syncRules(['dev-a']);
    await b.settle();
    const last = b.ruleSets.at(-1) as Json;
    expect(last.inherited).toEqual([
      { sessionId: 'a2', rule: expect.objectContaining({ id: deny, action: 'deny' }) },
    ]);
    // The parent's own rules stay in sessions; the allow stays with the parent.
    expect((last.sessions as Json[]).map((x) => x.sessionId)).toEqual(['a1', 'a1']);
  });

  it('follows the parent: a deny added later reaches the children at once, an allow never does', async () => {
    const b = await boot();
    await b.ready();
    b.store.recordChild('a2', ['a1'], Date.now(), 30 * 60_000);
    b.store.recordChild('a3', ['a1', 'a2'], Date.now(), 30 * 60_000);
    const before = b.store.permissionRevision('dev-a');
    const deny = await addRule(b, sessionRule('a1', 'deny'));
    await addRule(b, sessionRule('a1', 'allow', 'ls'));
    await b.settle();
    expect(b.store.permissionRevision('dev-a')).toBeGreaterThan(before);
    const inherited = (b.ruleSets.at(-1) as Json).inherited as Json[];
    expect(inherited.map((x) => [x.sessionId, x.rule.id]).sort()).toEqual([
      ['a2', deny],
      ['a3', deny],
    ]);
  });

  it('does not send a child the rules of a session that did not start it', async () => {
    const b = await boot();
    await b.ready();
    b.store.recordChild('a2', ['a1'], Date.now(), 30 * 60_000);
    await addRule(b, sessionRule('a3', 'deny'));
    await b.settle();
    expect((b.ruleSets.at(-1) as Json).inherited).toBeUndefined();
  });
});
