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
const CAPS = {
  permissions: true,
  interrupt: true,
  config: [
    {
      type: 'select',
      id: 'model',
      name: 'Model',
      category: 'model',
      currentValue: 'default',
      options: [
        { value: 'default', name: 'Default' },
        { value: 'opus', name: 'opus' },
      ],
    },
  ],
};
const claude = {
  id: 'claude',
  name: 'Claude Code',
  extends: 'claude',
  enabled: true,
  env: [],
  defaults: {},
};
const glm = { ...claude, id: 'glm', name: 'GLM', env: [{ name: 'TOKEN', set: true }] };
const PROVIDERS = [
  { provider: 'claude', configuration: claude, capabilities: CAPS },
  { provider: 'claude', configuration: glm, capabilities: CAPS },
];

type Msg = Record<string, unknown>;
const dirs: string[] = [];
const servers: Server[] = [];
const sockets: WebSocket[] = [];

afterEach(async () => {
  for (const ws of sockets.splice(0)) ws.terminate();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

async function boot() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-configurations-'));
  dirs.push(dir);
  const store = new Store(dir);
  const control = createControl(store, { workspaceTimeoutMs: 300 });
  const server = createServer();
  attachIngest(server, store, control);
  createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control).attach(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  servers.push(server);
  const port = (server.address() as AddressInfo).port;
  const call = async (method: string, p: string, body?: unknown) => {
    const res = await fetch(`http://127.0.0.1:${port}${p}`, {
      method,
      headers: { authorization: `Bearer ${HOST}`, 'content-type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    return { status: res.status, body: (await res.json()) as Msg };
  };
  const agent = async (deviceId: string, providers: unknown[] = PROVIDERS) => {
    const { token } = store.mintDevice(deviceId, deviceId);
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
      headers: { authorization: `Bearer ${token}` },
    });
    sockets.push(ws);
    const got: Msg[] = [];
    ws.on('message', (data) => got.push(JSON.parse(String(data)) as Msg));
    await new Promise<void>((r) => ws.once('open', () => r()));
    const send = (msg: Msg) => ws.send(JSON.stringify({ v: PROTOCOL_VERSION, ...msg }));
    send({ type: 'device.hello', deviceId, name: deviceId, version: 'test', providers });
    const waitFor = async (type: string): Promise<Msg> => {
      for (let i = 0; i < 100; i++) {
        const found = got.find((m) => m.type === type);
        if (found) return found;
        await new Promise((r) => setTimeout(r, 20));
      }
      throw new Error(`timed out waiting for ${type}`);
    };
    await waitFor('welcome');
    return { ws, send, waitFor };
  };
  return {
    store,
    post: (p: string, b?: unknown) => call('POST', p, b),
    get: (p: string) => call('GET', p),
    agent,
  };
}

const seed = (store: Store, configuration?: string) =>
  store.append(
    {
      sessionId: 's1',
      provider: 'claude',
      projectDir: 'p',
      filePath: '/s1',
      deviceId: 'dev-a',
      ...(configuration === undefined ? {} : { configuration }),
    },
    ['{"type":"user"}'],
  );

describe('the configuration of a session', () => {
  it('is stored with the owner announcement, replaced by the next and empty for the vendor own', () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-configurations-'));
    dirs.push(dir);
    const store = new Store(dir);
    seed(store);
    expect(store.sessionConfiguration('s1')).toBe('');
    seed(store, 'glm');
    expect(store.sessionConfiguration('s1')).toBe('glm');
    // A zero-line append is the title catch-up and carries it too.
    store.append(
      { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/s1', deviceId: 'dev-a' },
      [],
    );
    expect(store.sessionConfiguration('s1')).toBe('');
    seed(store, 'glm');
    expect(new Store(dir).sessionConfiguration('s1')).toBe('glm');
  });

  it('is named from the owner providers, by its id while the owner is offline, and absent for the vendor own', async () => {
    const { store, get, agent } = await boot();
    seed(store, 'glm');
    const listed = async () => (await get('/api/sessions/s1')).body;

    expect((await listed()).configuration).toEqual({ id: 'glm', name: 'glm' });
    const owner = await agent('dev-a');
    expect((await listed()).configuration).toEqual({ id: 'glm', name: 'GLM' });
    const all = (await get('/api/sessions')).body.sessions as Msg[];
    expect(all[0]?.configuration).toEqual({ id: 'glm', name: 'GLM' });
    owner.ws.close();

    seed(store, '');
    expect((await listed()).configuration).toBeUndefined();
  });

  it('follows a handoff to the configuration the target is asked for', () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-configurations-'));
    dirs.push(dir);
    const store = new Store(dir);
    seed(store, 'glm');
    const size = store.getSession('s1')?.byteSize ?? 0;
    expect(
      store.handoffOwner('s1', 'dev-a', 'dev-b', size, 0, '/w', 'hoplet/w', null, 'other'),
    ).toBe(1);
    expect(store.sessionConfiguration('s1')).toBe('other');
  });
});

describe('POST /api/devices/:id/providers', () => {
  it('relays the change and answers with the list the agent sent', async () => {
    const { post, agent } = await boot();
    const owner = await agent('dev-a');
    const pending = post('/api/devices/dev-a/providers', {
      id: 'glm',
      name: 'GLM 5',
      enabled: false,
      defaults: { model: 'opus' },
    });
    const update = await owner.waitFor('device.providers.update');
    expect(update).toMatchObject({
      id: 'glm',
      name: 'GLM 5',
      enabled: false,
      defaults: { model: 'opus' },
    });
    const changed = [
      PROVIDERS[0],
      { ...PROVIDERS[1], configuration: { ...glm, name: 'GLM 5', enabled: false } },
    ];
    owner.send({ type: 'device.providers', requestId: update.requestId, providers: changed });
    const res = await pending;
    expect(res.status).toBe(200);
    expect(res.body).toMatchObject({ deviceId: 'dev-a' });
    expect((res.body.providers as Msg[])[1]?.configuration).toMatchObject({
      name: 'GLM 5',
      enabled: false,
    });
  });

  it('relays the agent refusal', async () => {
    const { post, agent } = await boot();
    const owner = await agent('dev-a');
    const pending = post('/api/devices/dev-a/providers', { id: 'glm', enabled: false });
    const update = await owner.waitFor('device.providers.update');
    owner.send({
      type: 'device.providers.rejected',
      requestId: update.requestId,
      reason: 'read-only',
    });
    const res = await pending;
    expect(res.status).toBe(502);
    expect(res.body).toMatchObject({ reason: 'read-only' });
  });

  it('answers not-found, owner-offline, invalid-request and timeout', async () => {
    const { post, agent, store } = await boot();
    expect((await post('/api/devices/nobody/providers', { id: 'glm' })).status).toBe(404);
    store.mintDevice('off', 'dev-off');
    expect((await post('/api/devices/dev-off/providers', { id: 'glm' })).status).toBe(409);

    await agent('dev-a');
    expect((await post('/api/devices/dev-a/providers', { id: 'nope', enabled: true })).status).toBe(
      400,
    );
    expect((await post('/api/devices/dev-a/providers', { id: 'Bad Id' })).status).toBe(400);
    // The defaults must be options the driver declares.
    expect(
      (await post('/api/devices/dev-a/providers', { id: 'glm', defaults: { model: 'gpt' } }))
        .status,
    ).toBe(400);
    expect(
      (await post('/api/devices/dev-a/providers', { id: 'glm', defaults: { other: true } })).status,
    ).toBe(400);
    // An agent that never answers.
    expect((await post('/api/devices/dev-a/providers', { id: 'glm', enabled: true })).status).toBe(
      504,
    );
  });
});

describe('POST /api/sessions/:id/handoff with a configuration', () => {
  it('refuses a configuration the target does not offer or has disabled', async () => {
    const { post, agent, store } = await boot();
    seed(store);
    await agent('dev-a');
    await agent('dev-b', [
      { provider: 'claude', configuration: claude, capabilities: CAPS },
      { provider: 'claude', configuration: { ...glm, enabled: false }, capabilities: CAPS },
    ]);
    for (const configuration of ['nope', 'glm']) {
      const res = await post('/api/sessions/s1/handoff', {
        targetDeviceId: 'dev-b',
        configuration,
      });
      expect(res.status).toBe(400);
    }
    expect(
      (await post('/api/sessions/s1/handoff', { targetDeviceId: 'dev-b', configuration: 'Bad' }))
        .status,
    ).toBe(400);
  });
});
