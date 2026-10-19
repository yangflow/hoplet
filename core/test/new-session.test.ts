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
  { type: 'boolean', id: 'fast', name: 'Fast', category: 'model_config', currentValue: false },
];
const CAPS = { permissions: true, interrupt: true, config: CONFIG };
const PROVIDERS = [{ provider: 'claude', capabilities: CAPS }];
const PROJECT = '/work/app';
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
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-new-session-'));
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

const request = (over: Msg = {}) => ({
  deviceId: 'dev-a',
  projectPath: PROJECT,
  provider: 'claude',
  config: {},
  text: 'build the thing',
  promptId: 'n1',
  ...over,
});

describe('the session listing carries the runtime state', () => {
  it('is offline without the owner, idle with it, and what the owner reports', async () => {
    const { store, get, agent } = await boot();
    store.append(
      { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/s1', deviceId: 'dev-a' },
      ['{"type":"user"}'],
    );
    const listed = async () => ((await get('/api/sessions')).body.sessions as Msg[])[0];

    expect((await listed())?.runtime).toEqual({ state: 'offline' });
    const owner = await agent('dev-a');
    expect((await listed())?.runtime).toEqual({ state: 'idle' });
    owner.send({ type: 'runtime.state', sessionId: 's1', state: 'running', capabilities: CAPS });
    await new Promise((r) => setTimeout(r, 100));
    expect((await listed())?.runtime).toEqual({ state: 'running', capabilities: CAPS });
    owner.send({
      type: 'runtime.state',
      sessionId: 's1',
      state: 'failed',
      error: 'boom',
      capabilities: CAPS,
    });
    await new Promise((r) => setTimeout(r, 100));
    expect((await listed())?.runtime).toEqual({
      state: 'failed',
      error: 'boom',
      capabilities: CAPS,
    });
    owner.ws.close();
    await new Promise((r) => setTimeout(r, 100));
    expect((await listed())?.runtime).toEqual({ state: 'offline', capabilities: CAPS });
    expect((await get('/api/sessions/s1')).body.runtime).toMatchObject({ state: 'offline' });
  });
});

describe('GET /api/devices', () => {
  it('lists the providers a connected agent announced and none for an offline device', async () => {
    const { get, agent } = await boot();
    const owner = await agent('dev-a');
    await agent('dev-b', null);
    const devices = (await get('/api/devices')).body.devices as Msg[];
    expect(devices.find((d) => d.deviceId === 'dev-a')?.providers).toEqual(PROVIDERS);
    expect(devices.find((d) => d.deviceId === 'dev-b')?.providers).toEqual([]);
    owner.ws.close();
    await new Promise((r) => setTimeout(r, 100));
    const after = (await get('/api/devices')).body.devices as Msg[];
    expect(after.find((d) => d.deviceId === 'dev-a')).toMatchObject({
      connected: false,
      providers: [],
    });
  });
});

describe('POST /api/sessions', () => {
  it('refuses a malformed body, an unknown provider and an oversized prompt', async () => {
    const { post, agent } = await boot();
    const owner = await agent('dev-a');
    owner.listProjects();
    for (const bad of [
      { ...request(), text: '   ' },
      { ...request(), promptId: '' },
      { ...request(), provider: 'gemini' },
      { ...request(), config: 'x' },
      { ...request(), config: { thinking: 1 } },
      { deviceId: 'dev-a' },
    ]) {
      expect(await post('/api/sessions', bad), JSON.stringify(bad)).toEqual({
        status: 400,
        body: { code: 'invalid-request' },
      });
    }
    expect((await post('/api/sessions', request({ text: 'x'.repeat(8001) }))).status).toBe(413);
    expect(owner.ofType('runtime.start')).toEqual([]);
  });

  it('refuses target-offline when the device is not connected', async () => {
    const { post, store } = await boot();
    store.mintDevice('dev-a', 'dev-a');
    expect(await post('/api/sessions', request())).toEqual({
      status: 409,
      body: { code: 'target-offline', promptId: 'n1' },
    });
  });

  it('refuses a path that is not a watched project and config the device did not declare', async () => {
    const { post, agent } = await boot();
    const owner = await agent('dev-a');
    owner.listProjects();
    expect(await post('/api/sessions', request({ projectPath: '/elsewhere' }))).toEqual({
      status: 400,
      body: { code: 'invalid-request', promptId: 'n1', reason: 'projectPath' },
    });
    expect(
      await post('/api/sessions', request({ config: { model: 'gpt-9' }, promptId: 'n2' })),
    ).toEqual({
      status: 400,
      body: { code: 'invalid-request', promptId: 'n2', reason: 'config' },
    });
    expect(
      await post('/api/sessions', request({ config: { mode: 'plan' }, promptId: 'n3' })),
    ).toMatchObject({ status: 400, body: { reason: 'config' } });

    // A device whose driver declares nothing accepts no config.
    const bare = await agent('dev-b', [
      {
        provider: 'claude',
        capabilities: {
          permissions: true,
          interrupt: true,
          config: [],
        },
      },
    ]);
    bare.listProjects();
    for (const [i, config] of [{ thinking: true }, { fast: false }].entries()) {
      const res = await post(
        '/api/sessions',
        request({ deviceId: 'dev-b', config, promptId: `b${i}` }),
      );
      expect(res.status).toBe(400);
      expect(res.body).toMatchObject({ code: 'invalid-request' });
    }

    // A device that announced no providers cannot start anything.
    const silent = await agent('dev-c', null);
    silent.listProjects();
    expect(await post('/api/sessions', request({ deviceId: 'dev-c', promptId: 'c' }))).toEqual({
      status: 400,
      body: { code: 'invalid-request', promptId: 'c', reason: 'provider' },
    });
    expect(owner.ofType('runtime.start')).toEqual([]);
    expect(bare.ofType('runtime.start')).toEqual([]);
  });

  it('forwards runtime.start and answers with the session the agent reports', async () => {
    const { post, get, agent, store, control } = await boot();
    const owner = await agent('dev-a');
    owner.listProjects(['/other', `${PROJECT}/`]);
    const config = { model: 'opus', thinking: false, fast: true };
    const pending = post('/api/sessions', request({ config }));
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    expect(start).toMatchObject({
      v: PROTOCOL_VERSION,
      projectPath: `${PROJECT}/`,
      provider: 'claude',
      config,
      text: 'build the thing',
      promptId: 'n1',
    });
    expect(typeof start.requestId).toBe('string');

    owner.send({ type: 'runtime.started', requestId: start.requestId, sessionId: NEW_ID });
    expect(await pending).toEqual({
      status: 200,
      body: { ok: true, sessionId: NEW_ID, promptId: 'n1' },
    });

    // The agent reports the new session's state before the store has it, then syncs it.
    expect(control.providerOf(NEW_ID)).toBe('claude');
    owner.send({ type: 'runtime.state', sessionId: NEW_ID, state: 'running', capabilities: CAPS });
    store.append(
      {
        sessionId: NEW_ID,
        provider: 'claude',
        projectDir: 'p',
        filePath: `/${NEW_ID}`,
        deviceId: 'dev-a',
      },
      ['{"type":"user"}'],
    );
    await new Promise((r) => setTimeout(r, 100));
    expect((await get(`/api/sessions/${NEW_ID}`)).body.runtime).toEqual({
      state: 'running',
      capabilities: CAPS,
    });
  });

  it('reports a session running from what its agent said before it answered the start', async () => {
    const { post, get, agent, control } = await boot();
    const owner = await agent('dev-a');
    const other = await agent('dev-b');
    owner.listProjects();
    const pending = post('/api/sessions', request());
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    // The agent reports from its first moment, before the answer names the session: that is dropped, and it
    // reports again right behind the answer. The store has no line of the session yet. Another device's
    // report for the same session is not the owner's.
    owner.send({ type: 'runtime.state', sessionId: NEW_ID, state: 'running', capabilities: CAPS });
    other.send({
      type: 'runtime.state',
      sessionId: NEW_ID,
      state: 'failed',
      error: 'x',
      capabilities: CAPS,
    });
    const caps = { ...CAPS, steer: true };
    owner.send({ type: 'runtime.started', requestId: start.requestId, sessionId: NEW_ID });
    owner.send({ type: 'runtime.state', sessionId: NEW_ID, state: 'running', capabilities: caps });
    await pending;
    await new Promise((r) => setTimeout(r, 100));
    expect(control.runtimeOf(NEW_ID)).toEqual({ state: 'running', capabilities: caps });
    expect((await get(`/api/sessions/${NEW_ID}`)).status).toBe(404);

    // Its first turn takes a steer, and the steer goes to the device that started it.
    const steered = post(`/api/sessions/${NEW_ID}/steer`, { steerId: 'x1', text: 'also this' });
    const steer = await owner.waitFor((m) => m.type === 'runtime.steer', 'runtime.steer');
    expect(steer).toMatchObject({ sessionId: NEW_ID, steerId: 'x1', text: 'also this' });
    expect(other.ofType('runtime.steer')).toEqual([]);
    owner.send({ type: 'runtime.steered', sessionId: NEW_ID, steerId: 'x1' });
    expect((await steered).status).toBe(202);
  });

  it('ignores runtime messages of a device that did not start the session', async () => {
    const { post, get, agent, store } = await boot();
    const owner = await agent('dev-a');
    const other = await agent('dev-b');
    owner.listProjects();
    const pending = post('/api/sessions', request());
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    other.send({
      type: 'runtime.state',
      sessionId: NEW_ID,
      state: 'failed',
      error: 'x',
      capabilities: CAPS,
    });
    owner.send({ type: 'runtime.started', requestId: start.requestId, sessionId: NEW_ID });
    await pending;
    other.send({
      type: 'runtime.state',
      sessionId: NEW_ID,
      state: 'failed',
      error: 'x',
      capabilities: CAPS,
    });
    owner.send({ type: 'runtime.state', sessionId: NEW_ID, state: 'ready', capabilities: CAPS });
    store.append(
      {
        sessionId: NEW_ID,
        provider: 'claude',
        projectDir: 'p',
        filePath: `/${NEW_ID}`,
        deviceId: 'dev-a',
      },
      ['{"type":"user"}'],
    );
    await new Promise((r) => setTimeout(r, 100));
    expect((await get(`/api/sessions/${NEW_ID}`)).body.runtime).toMatchObject({ state: 'ready' });
  });

  it('returns the first result for a repeated promptId and forwards once', async () => {
    const { post, agent } = await boot();
    const owner = await agent('dev-a');
    owner.listProjects();
    const first = post('/api/sessions', request());
    const repeat = post('/api/sessions', request());
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    owner.send({ type: 'runtime.started', requestId: start.requestId, sessionId: NEW_ID });
    const expected = { status: 200, body: { ok: true, sessionId: NEW_ID, promptId: 'n1' } };
    expect(await first).toEqual(expected);
    expect(await repeat).toEqual(expected);
    expect(await post('/api/sessions', request())).toEqual(expected);
    expect(owner.ofType('runtime.start')).toHaveLength(1);
  });

  it('relays a rejection with the agent reason and lets the same promptId retry', async () => {
    const { post, agent } = await boot();
    const owner = await agent('dev-a');
    owner.listProjects();
    const first = post('/api/sessions', request());
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    owner.send({
      type: 'runtime.start.rejected',
      requestId: start.requestId,
      reason: 'the process exited',
    });
    expect(await first).toEqual({
      status: 502,
      body: { code: 'rejected', reason: 'the process exited', promptId: 'n1' },
    });

    const retry = post('/api/sessions', request());
    await owner.waitFor((m) => m.type === 'runtime.start' && m !== start, 'second runtime.start');
    const second = owner.ofType('runtime.start')[1] as Msg;
    owner.send({ type: 'runtime.started', requestId: second.requestId, sessionId: NEW_ID });
    expect((await retry).status).toBe(200);
  });

  it('ignores an answer that comes from another connection', async () => {
    const { post, agent } = await boot({ startTimeoutMs: 300 });
    const owner = await agent('dev-a');
    const other = await agent('dev-b');
    owner.listProjects();
    const pending = post('/api/sessions', request());
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    other.send({ type: 'runtime.started', requestId: start.requestId, sessionId: NEW_ID });
    expect((await pending).status).toBe(504);
  });

  it('times out when the agent never answers, and repeats that result for the promptId', async () => {
    const { post, agent } = await boot();
    const owner = await agent('dev-a');
    owner.listProjects();
    expect(await post('/api/sessions', request())).toEqual({
      status: 504,
      body: { code: 'timeout', promptId: 'n1' },
    });
    expect((await post('/api/sessions', request())).status).toBe(504);
    expect(owner.ofType('runtime.start')).toHaveLength(1);
  });

  it('answers target-offline when the agent disconnects while starting', async () => {
    const { post, agent } = await boot({ startTimeoutMs: 5000 });
    const owner = await agent('dev-a');
    owner.listProjects();
    const pending = post('/api/sessions', request());
    await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    owner.ws.close();
    expect(await pending).toEqual({
      status: 409,
      body: { code: 'target-offline', promptId: 'n1' },
    });
  });

  it('requires the host token', async () => {
    const { base } = await boot();
    const res = await fetch(`${base}/api/sessions`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(request()),
    });
    expect(res.status).toBe(401);
  });
});

describe('images for a prompt (ADR 0017)', () => {
  const IMAGE_PROVIDERS = [{ provider: 'claude', capabilities: { ...CAPS, images: true } }];
  const png = (size = 32) => {
    const bytes = Buffer.alloc(size);
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]).copy(bytes);
    return bytes.toString('base64');
  };
  const jpeg = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0, 0x10]).toString('base64');
  const MiB = 1024 * 1024;

  /** Answers every attachment.put as stored, or as rejected when `refuse` is set. */
  function storing(peer: Peer, refuse?: string): void {
    peer.ws.on('message', (data) => {
      const m = JSON.parse(String(data)) as Msg;
      if (m.type !== 'attachment.put' || (!m.last && !refuse)) return;
      peer.send(
        refuse
          ? { type: 'attachment.rejected', requestId: m.requestId, reason: refuse }
          : { type: 'attachment.stored', requestId: m.requestId, attachmentId: m.attachmentId },
      );
    });
  }

  it('uploads an image to the device on the bulk lane and names it', async () => {
    const { post, agent } = await boot();
    const owner = await agent('dev-a', IMAGE_PROVIDERS);
    storing(owner);
    const res = await post('/api/devices/dev-a/attachments', {
      mediaType: 'image/png',
      data: png(),
    });
    expect(res.status).toBe(200);
    const put = owner.ofType('attachment.put')[0];
    expect(put).toMatchObject({ mediaType: 'image/png', seq: 0, last: true, data: png() });
    expect(res.body).toEqual({ attachmentId: put?.attachmentId });

    // A larger image goes in chunks of 1 MiB, as a snapshot does, under one ID and in order.
    const big = png(2.5 * MiB);
    await post('/api/devices/dev-a/attachments', { mediaType: 'image/png', data: big });
    const chunks = owner.ofType('attachment.put').slice(1);
    expect(chunks.map((c) => [c.seq, c.last])).toEqual([
      [0, false],
      [1, false],
      [2, true],
    ]);
    expect(new Set(chunks.map((c) => c.attachmentId)).size).toBe(1);
    expect(chunks.every((c) => Buffer.from(String(c.data), 'base64').length <= MiB)).toBe(true);
    // Buffer.equals, not toEqual: a deep compare of megabytes takes seconds.
    const joined = Buffer.concat(chunks.map((c) => Buffer.from(String(c.data), 'base64')));
    expect(joined.equals(Buffer.from(big, 'base64'))).toBe(true);
  });

  it('refuses an upload that is not what it claims, too large, unwanted or refused', async () => {
    const { post, agent } = await boot();
    const owner = await agent('dev-a', IMAGE_PROVIDERS);
    storing(owner, 'invalid-image');
    const upload = (image: unknown, device = 'dev-a') =>
      post(`/api/devices/${device}/attachments`, image);
    expect(await upload({ mediaType: 'image/png', data: jpeg })).toMatchObject({
      status: 400,
      body: { reason: 'images' },
    });
    expect((await upload({ mediaType: 'image/png', data: 'not base64!' })).status).toBe(400);
    expect((await upload({ mediaType: 'image/svg+xml', data: png() })).status).toBe(400);
    expect((await upload({ mediaType: 'image/png', data: png(4 * MiB + 1) })).status).toBe(413);
    expect((await upload({ mediaType: 'image/png', data: png() }, 'dev-x')).status).toBe(409);
    // The device refused it: the reason comes back.
    expect(await upload({ mediaType: 'image/png', data: png() })).toMatchObject({
      status: 400,
      body: { reason: 'invalid-image' },
    });
    // A device whose providers take no images gets none.
    const plain = await boot();
    await plain.agent('dev-a');
    expect(
      await plain.post('/api/devices/dev-a/attachments', { mediaType: 'image/png', data: png() }),
    ).toMatchObject({ status: 400, body: { reason: 'images' } });
  });

  it('forwards the attachments a prompt names, only to a driver that takes images', async () => {
    const env = await boot();
    const owner = await env.agent('dev-a', IMAGE_PROVIDERS);
    env.store.append(
      { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/s1', deviceId: 'dev-a' },
      ['{"type":"user"}'],
    );
    const pending = env.post('/api/sessions/s1/prompt', {
      text: 'look at this',
      promptId: 'p1',
      attachments: ['a1', 'a2'],
    });
    const forwarded = await owner.waitFor((m) => m.type === 'runtime.prompt', 'runtime.prompt');
    expect(forwarded).toMatchObject({ sessionId: 's1', attachments: ['a1', 'a2'] });
    expect(JSON.stringify(forwarded).length).toBeLessThan(512);
    owner.send({ type: 'runtime.state', sessionId: 's1', state: 'running', capabilities: CAPS });
    expect((await pending).status).toBe(202);

    const tooMany = await env.post('/api/sessions/s1/prompt', {
      text: 'x',
      promptId: 'p2',
      attachments: ['a', 'b', 'c', 'd', 'e'],
    });
    expect(tooMany.status).toBe(400);

    const plain = await boot();
    const other = await plain.agent('dev-a');
    plain.store.append(
      { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/s1', deviceId: 'dev-a' },
      ['{"type":"user"}'],
    );
    expect(
      await plain.post('/api/sessions/s1/prompt', {
        text: 'x',
        promptId: 'p3',
        attachments: ['a1'],
      }),
    ).toMatchObject({ status: 400, body: { reason: 'images' } });
    expect(other.ofType('runtime.prompt')).toEqual([]);
  });

  it('forwards the attachments of a first prompt when the provider declares images', async () => {
    const { post, agent } = await boot();
    const owner = await agent('dev-a', IMAGE_PROVIDERS);
    owner.listProjects();
    void post('/api/sessions', request({ attachments: ['a1'] }));
    const start = await owner.waitFor((m) => m.type === 'runtime.start', 'runtime.start');
    expect(start.attachments).toEqual(['a1']);

    const plain = await boot();
    const other = await plain.agent('dev-a');
    other.listProjects();
    expect(await plain.post('/api/sessions', request({ attachments: ['a1'] }))).toMatchObject({
      status: 400,
      body: { code: 'invalid-request', reason: 'images' },
    });
  });
});

describe('the config of a session', () => {
  async function owned(providers: unknown[] = PROVIDERS) {
    const env = await boot();
    const owner = await env.agent('dev-a', providers);
    env.store.append(
      { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/s1', deviceId: 'dev-a' },
      ['{"type":"user"}'],
    );
    return { ...env, owner };
  }
  const change = (configId: string, value: unknown) => ({ configId, value });

  it('is changed with one message, and comes back with the state', async () => {
    const { post, get, owner } = await owned();
    expect(await post('/api/sessions/s1/config', change('model', 'sonnet'))).toEqual({
      status: 200,
      body: { ok: true },
    });
    const forwarded = await owner.waitFor((m) => m.type === 'runtime.config', 'runtime.config');
    expect(forwarded).toMatchObject({ sessionId: 's1', configId: 'model', value: 'sonnet' });
    expect(await post('/api/sessions/s1/config', change('thinking', false))).toMatchObject({
      status: 200,
    });
    expect(owner.ofType('runtime.config')).toHaveLength(2);

    const now = CONFIG.map((o) => (o.id === 'model' ? { ...o, currentValue: 'sonnet' } : o));
    owner.send({
      type: 'runtime.state',
      sessionId: 's1',
      state: 'ready',
      capabilities: { ...CAPS, config: now },
    });
    for (let i = 0; i < 50; i++) {
      const rt = (await get('/api/sessions/s1')).body.runtime as { capabilities?: unknown };
      if (rt?.capabilities && JSON.stringify(rt.capabilities).includes('"sonnet"')) break;
      await new Promise((r) => setTimeout(r, 20));
    }
    const runtime = (await get('/api/sessions/s1')).body.runtime as {
      capabilities: { config: { id: string; currentValue: unknown }[] };
      options?: unknown;
    };
    expect(runtime.capabilities.config.find((o) => o.id === 'model')?.currentValue).toBe('sonnet');
    expect(runtime.options).toBeUndefined();
  });

  it('changes a permission mode the driver declares, Bypass included, and refuses one it does not', async () => {
    const MODE = {
      type: 'select',
      id: 'mode',
      name: 'Permission mode',
      category: 'mode',
      currentValue: 'default',
      options: [
        { value: 'default', name: 'Manual' },
        { value: 'plan', name: 'Plan' },
        { value: 'bypassPermissions', name: 'Bypass permissions', risk: 'high' },
      ],
    };
    const { post, owner } = await owned([
      { provider: 'claude', capabilities: { ...CAPS, config: [MODE, ...CONFIG] } },
    ]);
    for (const value of ['plan', 'bypassPermissions']) {
      expect(await post('/api/sessions/s1/config', change('mode', value))).toEqual({
        status: 200,
        body: { ok: true },
      });
    }
    expect(owner.ofType('runtime.config').map((m) => m.value)).toEqual([
      'plan',
      'bypassPermissions',
    ]);
    expect(await post('/api/sessions/s1/config', change('mode', 'dontAsk'))).toMatchObject({
      status: 400,
      body: { reason: 'config' },
    });
    expect(owner.ofType('runtime.config')).toHaveLength(2);
  });

  it('is refused when the driver does not declare the option or the value', async () => {
    const { post, owner } = await owned();
    for (const bad of [
      change('model', 'gpt-9'),
      change('model', true),
      change('thinking', 'yes'),
      change('mode', 'plan'),
    ]) {
      expect(await post('/api/sessions/s1/config', bad), JSON.stringify(bad)).toMatchObject({
        status: 400,
        body: { code: 'invalid-request', reason: 'config' },
      });
    }
    expect(await post('/api/sessions/s1/config', { configId: '', value: 'x' })).toMatchObject({
      status: 400,
      body: { code: 'invalid-request' },
    });
    expect(owner.ofType('runtime.config')).toEqual([]);
  });

  it('is refused for an offline owner and for an external session', async () => {
    const { post, owner } = await owned();
    owner.send({ type: 'runtime.state', sessionId: 's1', state: 'external', capabilities: CAPS });
    await new Promise((r) => setTimeout(r, 100));
    expect((await post('/api/sessions/s1/config', change('fast', true))).status).toBeGreaterThan(
      399,
    );
    expect(owner.ofType('runtime.config')).toEqual([]);

    const { post: post2 } = await boot();
    expect(await post2('/api/sessions/nope/config', change('fast', true))).toMatchObject({
      status: 404,
    });
  });
});

describe('a prompt', () => {
  it('carries no options', async () => {
    const env = await boot();
    const owner = await env.agent('dev-a', PROVIDERS);
    env.store.append(
      { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/s1', deviceId: 'dev-a' },
      ['{"type":"user"}'],
    );
    const pending = env.post('/api/sessions/s1/prompt', { text: 'x', promptId: 'p1' });
    const forwarded = await owner.waitFor((m) => m.type === 'runtime.prompt', 'runtime.prompt');
    expect(forwarded.options).toBeUndefined();
    owner.send({ type: 'runtime.state', sessionId: 's1', state: 'starting', capabilities: CAPS });
    expect((await pending).status).toBe(202);
  });
});
