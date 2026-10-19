import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createApi } from '../src/api/index.ts';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

const HOST = 'host-token-0123456789abcdef';
const CONFIG = [
  {
    type: 'select',
    id: 'model',
    name: 'Model',
    category: 'model',
    currentValue: 'default',
    options: [
      { value: 'default', name: 'Default model' },
      { value: 'opus', name: 'opus' },
      { value: 'sonnet', name: 'sonnet' },
    ],
  },
  {
    type: 'boolean',
    id: 'thinking',
    name: 'Thinking',
    category: 'thought_level',
    currentValue: true,
  },
  {
    type: 'select',
    id: 'mode',
    name: 'Mode',
    category: 'mode',
    currentValue: 'acceptEdits',
    options: [
      { value: 'acceptEdits', name: 'Accept edits' },
      { value: 'default', name: 'Default' },
      { value: 'auto', name: 'Auto' },
    ],
  },
  { type: 'boolean', id: 'fast', name: 'Fast', category: 'model_config', currentValue: false },
];
const CAPS = { permissions: true, interrupt: true, config: CONFIG };
const PROVIDERS = [{ provider: 'claude', capabilities: CAPS }];
const PROJECT = '/work/app';
const OLD = 'old-session';
const NEW_ID = '3b1f1b0e-3c1a-4d6e-9a53-0c6f0f3f2a11';

const dirs: string[] = [];
const servers: Server[] = [];
const sockets: Peer[] = [];

afterEach(async () => {
  for (const p of sockets.splice(0)) p.ws.terminate();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

type Msg = Record<string, unknown>;

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
  /** Answers every project listing with one watched project. */
  listProjects(paths: string[] = [PROJECT]): void {
    this.ws.on('message', (data) => {
      const m = JSON.parse(String(data)) as Msg;
      if (m.type !== 'device.projects.list') return;
      this.send({
        type: 'device.projects',
        requestId: m.requestId,
        candidates: [],
        projects: paths.map((p) => ({ path: p, sessions: [] })),
      });
    });
  }
}

function open(url: string, headers: Record<string, string>): Promise<Peer> {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(url, { headers });
    const peer = new Peer(ws);
    sockets.push(peer);
    ws.once('open', () => resolve(peer));
    ws.once('error', reject);
  });
}

async function boot(options: { startTimeoutMs?: number } = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-fresh-'));
  dirs.push(dir);
  const store = new Store(dir);
  const control = createControl(store, { startTimeoutMs: 400, ...options });
  const server = createServer();
  attachIngest(server, store, control);
  createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control).attach(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  servers.push(server);
  const port = (server.address() as AddressInfo).port;
  const base = `http://127.0.0.1:${port}`;
  const call = async (method: string, p: string, body?: unknown) => {
    const res = await fetch(base + p, {
      method,
      headers: { authorization: `Bearer ${HOST}`, 'content-type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    return { status: res.status, body: (await res.json()) as Msg };
  };
  store.append(
    { sessionId: OLD, provider: 'claude', projectDir: 'p', filePath: `/${OLD}`, deviceId: 'dev-a' },
    [`{"type":"user","cwd":"${PROJECT}"}`],
  );
  const agent = async (deviceId: string, providers: unknown[] | null = PROVIDERS) => {
    const { token } = store.mintDevice(deviceId, deviceId);
    const peer = await open(`ws://127.0.0.1:${port}/ws/agent`, {
      authorization: `Bearer ${token}`,
    });
    peer.send({
      type: 'device.hello',
      deviceId,
      name: deviceId,
      version: 'test',
      ...(providers === null ? {} : { providers }),
    });
    await peer.waitFor((m) => m.type === 'welcome', 'welcome');
    return peer;
  };
  return {
    base,
    store,
    control,
    post: (p: string, b?: unknown) => call('POST', p, b),
    get: (p: string) => call('GET', p),
    agent,
  };
}

const PLAN = {
  plan: '1. add the route\n2. test it',
  planFile: '/home/u/.claude/plans/x.md',
  option: 'mode',
  modes: ['acceptEdits', 'default', 'auto'],
  default: 'acceptEdits',
  freshContext: true,
};
const FRESH = { requestId: 'p1', mode: 'auto', promptId: 'f1' };
const URL = `/api/sessions/${OLD}/permission/fresh`;

/** An owner with the old session waiting on a plan request, and every message the core publishes. */
async function setup(planExit: Msg = PLAN, options: { startTimeoutMs?: number } = {}) {
  const b = await boot(options);
  const owner = await b.agent('dev-a');
  owner.listProjects();
  const published: Msg[] = [];
  b.control.subscribe((m) => published.push(m as unknown as Msg));
  owner.send({
    type: 'runtime.state',
    sessionId: OLD,
    state: 'waiting-permission',
    capabilities: CAPS,
  });
  owner.send({
    type: 'runtime.permission',
    sessionId: OLD,
    requestId: 'p1',
    tool: 'ExitPlanMode',
    input: { plan: PLAN.plan },
    planExit,
  });
  for (let i = 0; i < 50 && b.control.openPermissions(OLD).length === 0; i++) {
    await new Promise((r) => setTimeout(r, 20));
  }
  return { ...b, owner, published };
}

const answers = (owner: Peer) => owner.ofType('runtime.permission.answer');

describe('POST /api/sessions/:id/permission/fresh', () => {
  it('starts the new session, links it, refuses the old plan and interrupts the old turn', async () => {
    const { post, owner, store, published } = await setup();
    const pending = post(URL, FRESH);
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    expect(start).toMatchObject({
      projectPath: PROJECT,
      provider: 'claude',
      config: { mode: 'auto' },
      promptId: 'f1',
    });
    const text = String(start.text);
    expect(text).toContain(PLAN.plan);
    expect(text.endsWith(`Plan file: ${PLAN.planFile}`)).toBe(true);
    // Nothing changes in the old session until the new one has started.
    expect(answers(owner)).toEqual([]);
    expect(store.continuationLinks(OLD)).toEqual({});

    owner.send({ type: 'runtime.started', requestId: start.requestId, sessionId: NEW_ID });
    expect(await pending).toEqual({
      status: 200,
      body: { ok: true, sessionId: NEW_ID, promptId: 'f1' },
    });
    expect(store.continuationLinks(OLD)).toEqual({ continuedIn: NEW_ID });
    expect(
      await owner.waitFor((m) => m.type === 'runtime.permission.answer', 'answer'),
    ).toMatchObject({
      sessionId: OLD,
      requestId: 'p1',
      decision: 'deny',
      message: 'This plan is being implemented in a new session.',
    });
    await owner.waitFor((m) => m.type === 'runtime.interrupt', 'runtime.interrupt');
    const updated = published.filter((m) => m.type === 'session.updated').map((m) => m.sessionId);
    expect(updated).toEqual(expect.arrayContaining([OLD, NEW_ID]));
  });

  it('omits the plan file line when the request has none', async () => {
    const { planFile: _omit, ...noFile } = PLAN;
    const { post, owner } = await setup(noFile);
    const pending = post(URL, FRESH);
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    expect(String(start.text)).not.toContain('Plan file:');
    owner.send({ type: 'runtime.started', requestId: start.requestId, sessionId: NEW_ID });
    expect((await pending).status).toBe(200);
  });

  it('refuses what the request cannot take, before starting anything', async () => {
    const { post, owner } = await setup();
    expect(await post(URL, { ...FRESH, requestId: 'nope' })).toEqual({
      status: 409,
      body: { code: 'request-closed', promptId: 'f1' },
    });
    expect(await post(URL, { ...FRESH, mode: 'bypassPermissions', promptId: 'f2' })).toEqual({
      status: 400,
      body: { code: 'invalid-request', promptId: 'f2', reason: 'mode' },
    });
    expect((await post('/api/sessions/missing/permission/fresh', FRESH)).body).toMatchObject({
      code: 'session-not-found',
    });
    expect(owner.ofType('runtime.start')).toEqual([]);
    expect(answers(owner)).toEqual([]);
  });

  it('refuses a plan the provider cannot start fresh from', async () => {
    const { freshContext: _omit, ...plain } = PLAN;
    const { post, owner } = await setup(plain);
    expect(await post(URL, FRESH)).toEqual({
      status: 400,
      body: { code: 'invalid-request', promptId: 'f1', reason: 'fresh-context' },
    });
    expect(owner.ofType('runtime.start')).toEqual([]);
  });

  it('refuses a request that is not a plan exit', async () => {
    const { post, owner, control } = await setup();
    owner.send({
      type: 'runtime.permission',
      sessionId: OLD,
      requestId: 'b1',
      tool: 'Bash',
      input: { command: 'ls' },
    });
    for (let i = 0; i < 50 && control.openPermissions(OLD).length < 2; i++) {
      await new Promise((r) => setTimeout(r, 20));
    }
    expect((await post(URL, { ...FRESH, requestId: 'b1' })).body).toMatchObject({
      code: 'request-closed',
    });
  });

  it('refuses when the owner is offline', async () => {
    const { post, owner } = await setup();
    owner.ws.terminate();
    await new Promise((r) => setTimeout(r, 100));
    expect(await post(URL, FRESH)).toEqual({
      status: 409,
      body: { code: 'owner-offline', promptId: 'f1' },
    });
  });

  it('changes nothing when the start fails, and lets the same promptId retry', async () => {
    const { post, owner, store } = await setup();
    const first = post(URL, FRESH);
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    owner.send({
      type: 'runtime.start.rejected',
      requestId: start.requestId,
      reason: 'the process exited',
    });
    expect(await first).toEqual({
      status: 502,
      body: { code: 'rejected', reason: 'the process exited', promptId: 'f1' },
    });
    expect(store.continuationLinks(OLD)).toEqual({});
    expect(answers(owner)).toEqual([]);
    expect(owner.ofType('runtime.interrupt')).toEqual([]);

    const retry = post(URL, FRESH);
    await owner.waitFor((m) => m.type === 'runtime.start' && m !== start, 'second runtime.start');
    const second = owner.ofType('runtime.start')[1] as Msg;
    owner.send({ type: 'runtime.started', requestId: second.requestId, sessionId: NEW_ID });
    expect((await retry).status).toBe(200);
    expect(store.continuationLinks(OLD)).toEqual({ continuedIn: NEW_ID });
  });

  it('changes nothing when the start times out', async () => {
    const { post, owner, store } = await setup(PLAN, { startTimeoutMs: 150 });
    const result = await post(URL, FRESH);
    expect(result.status).toBe(504);
    expect(result.body).toMatchObject({ code: 'timeout', promptId: 'f1' });
    expect(owner.ofType('runtime.start')).toHaveLength(1);
    expect(store.continuationLinks(OLD)).toEqual({});
    expect(answers(owner)).toEqual([]);
  });

  it('keeps the link but neither refuses nor interrupts a plan answered during the start', async () => {
    const { post, owner, store } = await setup();
    const pending = post(URL, FRESH);
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    expect(
      (
        await post(`/api/sessions/${OLD}/permission`, {
          requestId: 'p1',
          decision: 'allow',
          mode: 'auto',
        })
      ).status,
    ).toBe(200);
    owner.send({ type: 'runtime.started', requestId: start.requestId, sessionId: NEW_ID });
    expect((await pending).status).toBe(200);
    expect(store.continuationLinks(OLD)).toEqual({ continuedIn: NEW_ID });
    await new Promise((r) => setTimeout(r, 100));
    expect(answers(owner).map((m) => m.decision)).toEqual(['allow']);
    expect(owner.ofType('runtime.interrupt')).toEqual([]);
  });

  it('refuses a second promptId for a plan whose new session is starting', async () => {
    const { post, owner } = await setup();
    const pending = post(URL, FRESH);
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    expect((await post(URL, { ...FRESH, promptId: 'f2' })).body).toMatchObject({
      code: 'request-closed',
      promptId: 'f2',
    });
    owner.send({ type: 'runtime.started', requestId: start.requestId, sessionId: NEW_ID });
    expect((await pending).status).toBe(200);
    expect(owner.ofType('runtime.start')).toHaveLength(1);
  });

  it('answers a repeated promptId from the first result without acting again', async () => {
    const { post, owner } = await setup();
    const first = post(URL, FRESH);
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    owner.send({ type: 'runtime.started', requestId: start.requestId, sessionId: NEW_ID });
    const expected = await first;
    expect(expected.status).toBe(200);
    await owner.waitFor((m) => m.type === 'runtime.permission.answer', 'answer');
    expect(await post(URL, FRESH)).toEqual(expected);
    await new Promise((r) => setTimeout(r, 100));
    expect(owner.ofType('runtime.start')).toHaveLength(1);
    expect(answers(owner)).toHaveLength(1);
    expect(owner.ofType('runtime.interrupt')).toHaveLength(1);
  });

  it('rejects a malformed body', async () => {
    const { post } = await setup();
    expect((await post(URL, { requestId: 'p1', mode: 'auto' })).body).toMatchObject({
      code: 'invalid-request',
    });
  });
});
