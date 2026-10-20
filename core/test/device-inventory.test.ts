import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  DEVICE_LABEL_MAX,
  type DeviceInfo,
  MAX_DEVICE_LABELS,
  PROTOCOL_VERSION,
  type ServerMessage,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { createApi } from '../src/api/index.ts';
import { type ControlReply, createControl } from '../src/control/index.ts';
import { parseFrame } from '../src/ingest/decode.ts';
import { Store } from '../src/store/index.ts';

const HOST = 'host-token-0123456789abcdef';

const dirs: string[] = [];
const servers: Server[] = [];
afterEach(async () => {
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

function tmp(): string {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-inventory-'));
  dirs.push(dir);
  return dir;
}

const inventory = {
  at: 1_700_000_000_000,
  toolchains: [
    { name: 'git', version: '2.45.1' },
    { name: 'node', version: '22.4.0' },
  ],
  gpus: [{ name: 'NVIDIA RTX 4090', memory: 24 * 1024 ** 3 }, { name: 'Apple M3 Pro' }],
  repositories: [
    { path: '/work/hoplet', origin: 'https://github.com/yangflow/hoplet.git' },
    { path: '/work/scratch', origin: null },
  ],
};
const report = (fields: Record<string, unknown> = {}) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'device.inventory',
    ...inventory,
    ...fields,
  }) as unknown as ControlReply;

function setup() {
  const dir = tmp();
  const store = new Store(dir);
  store.mintDevice('dev-a', 'dev-a');
  const control = createControl(store, {});
  const link = {
    send(_msg: ServerMessage) {},
    async sendAndWait(_msg: ServerMessage) {},
    isOpen: () => true,
  };
  return { dir, store, control, link };
}

const find = (devices: DeviceInfo[], id = 'dev-a') => devices.find((d) => d.deviceId === id);

describe('device.inventory', () => {
  it('is stored for the device that sent it and listed with it', () => {
    const { store, control, link } = setup();
    expect(find(control.devices())).toMatchObject({ labels: [] });
    expect(find(control.devices())?.inventory).toBeUndefined();
    control.attach('dev-a', link, []);
    control.receive(link, report());
    expect(find(control.devices())?.inventory).toEqual(inventory);
    expect(store.listDevices()[0]?.inventory).toEqual(inventory);
  });

  it('is replaced by the next report and kept while offline and after a restart', () => {
    const { dir, control, link } = setup();
    control.attach('dev-a', link, []);
    control.receive(link, report());
    control.receive(link, report({ at: 2, toolchains: [], gpus: [], repositories: [] }));
    control.detach(link);
    const listed = find(control.devices());
    expect(listed).toMatchObject({ connected: false });
    expect(listed?.inventory).toEqual({ at: 2, toolchains: [], gpus: [], repositories: [] });
    expect(find(createControl(new Store(dir), {}).devices())?.inventory?.at).toBe(2);
  });

  it('from a connection that is not attached to a device is dropped', () => {
    const { control, link } = setup();
    control.receive(link, report());
    expect(find(control.devices())?.inventory).toBeUndefined();
  });

  it('is refused by the decoder when a list is over its limit or a field is malformed', () => {
    const decode = (fields: Record<string, unknown>) =>
      parseFrame(
        JSON.stringify({ v: PROTOCOL_VERSION, type: 'device.inventory', ...inventory, ...fields }),
      );
    expect(decode({}).ok).toBe(true);
    const tools = Array.from({ length: 33 }, () => ({ name: 'a', version: '1' }));
    expect(decode({ toolchains: tools }).ok).toBe(false);
    expect(decode({ gpus: Array.from({ length: 9 }, () => ({ name: 'g' })) }).ok).toBe(false);
    const repos = Array.from({ length: 201 }, () => ({ path: '/p', origin: null }));
    expect(decode({ repositories: repos }).ok).toBe(false);
    expect(decode({ repositories: [{ path: '/p' }] }).ok).toBe(false);
    expect(decode({ gpus: [{ name: 'g', memory: -1 }] }).ok).toBe(false);
  });
});

describe('/api/devices/:deviceId/labels', () => {
  async function boot() {
    const { dir, store, control, link } = setup();
    const server = createServer();
    createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const port = (server.address() as AddressInfo).port;
    const call = async (method: string, url: string, body?: unknown, auth = true, raw?: string) => {
      const res = await fetch(`http://127.0.0.1:${port}${url}`, {
        method,
        headers: {
          ...(auth ? { authorization: `Bearer ${HOST}` } : {}),
          'content-type': 'application/json',
        },
        body: raw ?? (body === undefined ? null : JSON.stringify(body)),
      });
      const text = await res.text();
      return { status: res.status, body: text ? JSON.parse(text) : {} };
    };
    const put = (labels: unknown, id = 'dev-a') =>
      call('PUT', `/api/devices/${id}/labels`, { labels });
    return { call, put, control, link };
  }

  it('needs the host token', async () => {
    const { call } = await boot();
    expect((await call('PUT', '/api/devices/dev-a/labels', { labels: [] }, false)).status).toBe(
      401,
    );
  });

  it('replaces the labels, drops duplicates keeping the order and lists them while offline', async () => {
    const { call, put } = await boot();
    expect((await call('GET', '/api/devices')).body.devices[0]).toMatchObject({ labels: [] });
    expect(await put(['gpu', 'home-lab', 'gpu', 'a1'])).toMatchObject({
      status: 200,
      body: { labels: ['gpu', 'home-lab', 'a1'] },
    });
    const listed = (await call('GET', '/api/devices')).body.devices[0];
    expect(listed).toMatchObject({ connected: false, labels: ['gpu', 'home-lab', 'a1'] });
    expect((await put([])).body).toEqual({ labels: [] });
    expect((await call('GET', '/api/devices')).body.devices[0].labels).toEqual([]);
  });

  it('keeps the labels when the agent connects and reports', async () => {
    const { call, put, control, link } = await boot();
    await put(['gpu']);
    control.attach('dev-a', link, []);
    control.receive(link, report());
    const listed = (await call('GET', '/api/devices')).body.devices[0];
    expect(listed).toMatchObject({ connected: true, labels: ['gpu'], inventory });
  });

  it('accepts the longest label and the most labels', async () => {
    const { put } = await boot();
    const long = `a${'-'.repeat(DEVICE_LABEL_MAX - 1)}`;
    expect((await put([long])).status).toBe(200);
    const many = Array.from({ length: MAX_DEVICE_LABELS }, (_, i) => `l${i}`);
    expect((await put(many)).body).toEqual({ labels: many });
  });

  it('refuses malformed JSON, a bad label and more than 20 labels', async () => {
    const { call, put } = await boot();
    expect(await call('PUT', '/api/devices/dev-a/labels', undefined, true, '{')).toMatchObject({
      status: 400,
      body: { code: 'invalid-json' },
    });
    const bad: unknown[] = [
      ['Upper'],
      ['has space'],
      [''],
      ['-lead'],
      ['x'.repeat(DEVICE_LABEL_MAX + 1)],
      ['ünï'],
      [1],
      'gpu',
      Array.from({ length: MAX_DEVICE_LABELS + 1 }, (_, i) => `l${i}`),
    ];
    for (const labels of bad) {
      expect(await put(labels)).toMatchObject({ status: 400, body: { code: 'invalid-request' } });
    }
    expect(await call('PUT', '/api/devices/dev-a/labels', {})).toMatchObject({ status: 400 });
  });

  it('refuses a body over 16 KiB and an unknown device', async () => {
    const { call, put } = await boot();
    const huge = { labels: [], pad: 'x'.repeat(20_000) };
    expect(await call('PUT', '/api/devices/dev-a/labels', huge)).toMatchObject({
      status: 413,
      body: { code: 'too-large' },
    });
    expect(await put(['gpu'], 'nobody')).toMatchObject({
      status: 404,
      body: { code: 'not-found' },
    });
  });
});
