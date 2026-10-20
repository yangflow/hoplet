import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  type DeviceResourceSample,
  MAX_RESOURCE_THRESHOLD_MACHINES,
  PROTOCOL_VERSION,
  type ResourceThresholds,
  type ServerMessage,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { createApi } from '../src/api/index.ts';
import { type ControlReply, createControl } from '../src/control/index.ts';
import { resourceWarnings } from '../src/control/resources.ts';
import { Store } from '../src/store/index.ts';

const HOST = 'host-token-0123456789abcdef';
const GB = 1024 ** 3;
const DEFAULTS: ResourceThresholds = { default: { memory: 90, disk: 10 }, machines: {} };

const dirs: string[] = [];
const servers: Server[] = [];
afterEach(async () => {
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

function tmp(): string {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-thresholds-'));
  dirs.push(dir);
  return dir;
}

const reading = (memUsed: number, diskFree: number): DeviceResourceSample => ({
  at: 1,
  cpu: 1,
  memUsed,
  memTotal: 100,
  diskFree,
  diskTotal: 100,
});

describe('resourceWarnings', () => {
  it('uses 90% memory and 10% disk without thresholds', () => {
    expect(resourceWarnings(reading(90, 10))).toEqual({ memory: false, disk: false });
    expect(resourceWarnings(reading(91, 9))).toEqual({ memory: true, disk: true });
  });

  it('uses the thresholds it is given', () => {
    const t = { memory: 50, disk: 40 };
    expect(resourceWarnings(reading(50, 40), t)).toEqual({ memory: false, disk: false });
    expect(resourceWarnings(reading(51, 39), t)).toEqual({ memory: true, disk: true });
    expect(resourceWarnings(reading(60, 80), { memory: 100, disk: 0 })).toEqual({
      memory: false,
      disk: false,
    });
  });

  it('never warns about a memory or volume of size 0', () => {
    expect(resourceWarnings({ ...reading(0, 0), memTotal: 0, diskTotal: 0 })).toEqual({
      memory: false,
      disk: false,
    });
  });
});

describe('resource thresholds in the store', () => {
  it('are the defaults while none are stored', () => {
    expect(new Store(tmp()).resourceThresholds()).toEqual(DEFAULTS);
  });

  it('round-trip and survive a restart', () => {
    const dir = tmp();
    const next: ResourceThresholds = {
      default: { memory: 80, disk: 20 },
      machines: { 'dev-a': { memory: 95, disk: 5 } },
    };
    new Store(dir).setResourceThresholds(next);
    expect(new Store(dir).resourceThresholds()).toEqual(next);
  });
});

describe('warnings in the device list', () => {
  function setup() {
    const store = new Store(tmp());
    store.mintDevice('dev-a', 'dev-a');
    store.mintDevice('dev-b', 'dev-b');
    const control = createControl(store, {});
    const link = {
      send(_msg: ServerMessage) {},
      async sendAndWait(_msg: ServerMessage) {},
      isOpen: () => true,
    };
    control.attach('dev-a', link, []);
    return { store, control, link };
  }
  const report = (memUsed: number) =>
    ({
      v: PROTOCOL_VERSION,
      type: 'device.resources',
      at: 1,
      cpu: 1,
      memUsed: memUsed * GB,
      memTotal: 100 * GB,
      diskFree: 50 * GB,
      diskTotal: 100 * GB,
      sessions: [],
    }) as unknown as ControlReply;
  const warn = (c: ReturnType<typeof setup>['control']) =>
    c.devices().find((d) => d.deviceId === 'dev-a')?.warn;

  it('follow the default and a machine override as soon as they are stored', () => {
    const { store, control, link } = setup();
    control.receive(link, report(70));
    expect(warn(control)).toEqual({ memory: false, disk: false });
    store.setResourceThresholds({ default: { memory: 60, disk: 10 }, machines: {} });
    expect(warn(control)).toEqual({ memory: true, disk: false });
    store.setResourceThresholds({
      default: { memory: 60, disk: 10 },
      machines: { 'dev-a': { memory: 80, disk: 60 }, 'dev-b': { memory: 1, disk: 99 } },
    });
    expect(warn(control)).toEqual({ memory: false, disk: true });
  });
});

describe('/api/resources/thresholds', () => {
  async function boot() {
    const dir = tmp();
    const store = new Store(dir);
    const control = createControl(store, {});
    const server = createServer();
    createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const port = (server.address() as AddressInfo).port;
    return async (method: string, body?: unknown, auth = true, raw?: string) => {
      const res = await fetch(`http://127.0.0.1:${port}/api/resources/thresholds`, {
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
  }

  it('needs the host token', async () => {
    const call = await boot();
    expect((await call('GET', undefined, false)).status).toBe(401);
    expect((await call('PUT', DEFAULTS, false)).status).toBe(401);
  });

  it('answers the defaults, stores a replacement and drops an override by omitting it', async () => {
    const call = await boot();
    expect((await call('GET')).body).toEqual(DEFAULTS);
    const next: ResourceThresholds = {
      default: { memory: 85, disk: 15 },
      machines: { a: { memory: 100, disk: 0 }, b: { memory: 1, disk: 99 } },
    };
    expect((await call('PUT', next)).body).toEqual(next);
    expect((await call('GET')).body).toEqual(next);
    const fewer = { ...next, machines: { b: next.machines.b } };
    expect((await call('PUT', fewer)).body).toEqual(fewer);
    expect((await call('GET')).body).toEqual(fewer);
  });

  it('refuses malformed JSON and values out of range', async () => {
    const call = await boot();
    const bad = await call('PUT', undefined, true, '{');
    expect(bad).toMatchObject({ status: 400, body: { code: 'invalid-json' } });
    const cases: unknown[] = [
      { default: { memory: 0, disk: 10 }, machines: {} },
      { default: { memory: 101, disk: 10 }, machines: {} },
      { default: { memory: 90, disk: -1 }, machines: {} },
      { default: { memory: 90, disk: 100 }, machines: {} },
      { default: { memory: 90.5, disk: 10 }, machines: {} },
      { default: { memory: 90, disk: 10 } },
      { default: { memory: 90, disk: 10 }, machines: { a: { memory: 90 } } },
    ];
    for (const body of cases) {
      expect(await call('PUT', body)).toMatchObject({
        status: 400,
        body: { code: 'invalid-request' },
      });
    }
    expect((await call('GET')).body).toEqual(DEFAULTS);
  });

  it('refuses more overrides than the limit and bodies over 64 KiB', async () => {
    const call = await boot();
    const machines: ResourceThresholds['machines'] = {};
    for (let i = 0; i <= MAX_RESOURCE_THRESHOLD_MACHINES; i++) {
      machines[`dev-${i}`] = { memory: 90, disk: 10 };
    }
    expect(await call('PUT', { ...DEFAULTS, machines })).toMatchObject({
      status: 413,
      body: { code: 'too-large' },
    });
    const huge = {
      ...DEFAULTS,
      machines: { x: { memory: 90, disk: 10 } },
      pad: 'x'.repeat(70_000),
    };
    expect((await call('PUT', huge)).body).toMatchObject({ code: 'too-large' });
  });
});
