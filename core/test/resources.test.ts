import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  type DeviceInfo,
  PROTOCOL_VERSION,
  RESOURCES_RING_SIZE,
  type ServerMessage,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createApi } from '../src/api/index.ts';
import { type ControlReply, createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

const HOST = 'host-token-0123456789abcdef';
const GB = 1024 ** 3;

const dirs: string[] = [];
const servers: Server[] = [];
const sockets: WebSocket[] = [];
afterEach(async () => {
  for (const s of sockets.splice(0)) s.terminate();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

function fakeLink() {
  return {
    send(_msg: ServerMessage) {},
    async sendAndWait(_msg: ServerMessage) {},
    isOpen: () => true,
  };
}

function setup() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-resources-'));
  dirs.push(dir);
  const store = new Store(dir);
  store.mintDevice('dev-a', 'dev-a');
  const control = createControl(store, {});
  const link = fakeLink();
  control.attach('dev-a', link, []);
  return { control, link, store };
}

const sample = (at: number, extra: Record<string, unknown> = {}) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'device.resources',
    at,
    cpu: 12.5,
    memUsed: 4 * GB,
    memTotal: 16 * GB,
    diskFree: 200 * GB,
    diskTotal: 500 * GB,
    sessions: [],
    ...extra,
  }) as unknown as ControlReply;

const device = (control: ReturnType<typeof setup>['control'], id = 'dev-a'): DeviceInfo => {
  const found = control.devices().find((d) => d.deviceId === id);
  if (!found) throw new Error(`no device ${id}`);
  return found;
};

describe('device.resources', () => {
  it('shows the latest reading in the device list, with no warning while the machine is healthy', () => {
    const { control, link } = setup();
    expect(device(control).resources).toBeUndefined();
    expect(device(control).warn).toBeUndefined();
    control.receive(link, sample(1000));
    control.receive(link, sample(2000, { cpu: null }));
    expect(device(control).resources).toMatchObject({ at: 2000, cpu: null, memTotal: 16 * GB });
    expect(device(control).warn).toEqual({ memory: false, disk: false });
  });

  it('warns above 90% memory used and below 10% disk free, only at the thresholds crossed', () => {
    const { control, link } = setup();
    control.receive(link, sample(1, { memUsed: 0.9 * 16 * GB, diskFree: 0.1 * 500 * GB }));
    expect(device(control).warn).toEqual({ memory: false, disk: false });
    control.receive(link, sample(2, { memUsed: 15 * GB }));
    expect(device(control).warn).toEqual({ memory: true, disk: false });
    control.receive(link, sample(3, { diskFree: 10 * GB }));
    expect(device(control).warn).toEqual({ memory: false, disk: true });
  });

  it('keeps the last reading after a disconnect but stops warning', () => {
    const { control, link } = setup();
    control.receive(link, sample(5000, { memUsed: 16 * GB, diskFree: 1 * GB }));
    expect(device(control).warn).toEqual({ memory: true, disk: true });
    control.detach(link);
    expect(device(control).connected).toBe(false);
    expect(device(control).resources).toMatchObject({ at: 5000 });
    expect(device(control).warn).toEqual({ memory: false, disk: false });
    expect(control.resourcesOf('dev-a').samples).toHaveLength(1);
  });

  it('keeps at most the last hour of readings, oldest dropped', () => {
    const { control, link } = setup();
    for (let i = 0; i < RESOURCES_RING_SIZE + 10; i++) control.receive(link, sample(i));
    const { samples } = control.resourcesOf('dev-a');
    expect(samples).toHaveLength(RESOURCES_RING_SIZE);
    expect(samples[0]?.at).toBe(10);
    expect(samples.at(-1)?.at).toBe(RESOURCES_RING_SIZE + 9);
  });

  it('ignores a report from a connection no device is attached with', () => {
    const { control } = setup();
    control.receive(fakeLink(), sample(1));
    expect(control.resourcesOf('dev-a').samples).toEqual([]);
  });

  it('keeps the sessions of the latest report only', () => {
    const { control, link } = setup();
    const s = (sessionId: string, rss: number) => ({ sessionId, cpu: null, rss, children: 0 });
    control.receive(link, sample(1, { sessions: [s('a', 1)] }));
    control.receive(link, sample(2, { sessions: [s('b', 2)] }));
    expect(control.resourcesOf('dev-a').sessions.map((x) => x.sessionId)).toEqual(['b']);
  });
});

describe('GET /api/devices/:deviceId/resources', () => {
  async function boot() {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-resources-api-'));
    dirs.push(dir);
    const store = new Store(dir);
    store.append(
      { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/s1', deviceId: 'dev-a' },
      ['{"type":"user"}'],
    );
    const control = createControl(store, {});
    const server = createServer();
    attachIngest(server, store, control);
    createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const port = (server.address() as AddressInfo).port;
    const get = async (p: string, auth = true) => {
      const res = await fetch(`http://127.0.0.1:${port}${p}`, {
        headers: auth ? { authorization: `Bearer ${HOST}` } : {},
      });
      return { status: res.status, body: (await res.json()) as Record<string, unknown> };
    };
    const agent = async (deviceId: string) => {
      const { token } = store.mintDevice(deviceId, deviceId);
      const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
        headers: { authorization: `Bearer ${token}` },
      });
      sockets.push(ws);
      const got: Record<string, unknown>[] = [];
      ws.on('message', (d) => got.push(JSON.parse(String(d))));
      await new Promise<void>((r) => ws.once('open', () => r()));
      const send = (msg: Record<string, unknown>) =>
        ws.send(JSON.stringify({ v: PROTOCOL_VERSION, ...msg }));
      send({ type: 'device.hello', deviceId, name: deviceId, version: 'test' });
      for (let i = 0; i < 100 && !got.some((m) => m.type === 'welcome'); i++) {
        await new Promise((r) => setTimeout(r, 20));
      }
      return { send, got };
    };
    return { get, agent };
  }

  const frame = (extra: Record<string, unknown> = {}) => ({
    type: 'device.resources',
    at: 7000,
    cpu: 3,
    memUsed: 1,
    memTotal: 2,
    diskFree: 3,
    diskTotal: 4,
    sessions: [
      { sessionId: 'unknown-1', cpu: 1, rss: 10, children: 0 },
      { sessionId: 's1', cpu: null, rss: 500, children: 2 },
    ],
    ...extra,
  });
  const settle = () => new Promise((r) => setTimeout(r, 100));

  it('requires the host token', async () => {
    const { get } = await boot();
    expect((await get('/api/devices/dev-a/resources', false)).status).toBe(401);
  });

  it('answers 404 for an unknown device', async () => {
    const { get } = await boot();
    expect((await get('/api/devices/nobody/resources')).status).toBe(404);
  });

  it('answers empty for a device that never reported', async () => {
    const { get, agent } = await boot();
    await agent('dev-a');
    const res = await get('/api/devices/dev-a/resources');
    expect(res.status).toBe(200);
    expect(res.body).toEqual({ samples: [], sessions: [] });
  });

  it('lists the ring and the latest sessions by memory, naming the ones the store knows', async () => {
    const { get, agent } = await boot();
    const a = await agent('dev-a');
    a.send(frame());
    await settle();
    const res = await get('/api/devices/dev-a/resources');
    expect(res.status).toBe(200);
    expect(res.body.at).toBe(7000);
    expect(res.body.samples).toEqual([
      { at: 7000, cpu: 3, memUsed: 1, memTotal: 2, diskFree: 3, diskTotal: 4 },
    ]);
    const sessions = res.body.sessions as { sessionId: string; title: string | null }[];
    expect(sessions.map((s) => s.sessionId)).toEqual(['s1', 'unknown-1']);
    expect(sessions[0]?.title).not.toBeNull();
    expect(sessions[1]?.title).toBeNull();
    const list = (await get('/api/devices')).body.devices as DeviceInfo[];
    expect(list.find((d) => d.deviceId === 'dev-a')?.resources?.at).toBe(7000);
  });

  it('drops a malformed frame without keeping anything', async () => {
    const { get, agent } = await boot();
    const a = await agent('dev-a');
    a.send(frame({ cpu: 250 }));
    await settle();
    expect((await get('/api/devices/dev-a/resources')).body.samples).toEqual([]);
  });
});
