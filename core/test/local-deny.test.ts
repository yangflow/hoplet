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
const SESSION = '3b1f1b0e-3c1a-4d6e-9a53-0c6f0f3f2a11';

type Msg = Record<string, unknown>;
const dirs: string[] = [];
const servers: Server[] = [];
const sockets: WebSocket[] = [];

afterEach(async () => {
  for (const ws of sockets.splice(0)) ws.terminate();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

const denyRule = (id: string, over: Msg = {}) => ({
  id,
  tool: 'run',
  match: { command: 'curl' },
  action: 'deny',
  scope: 'agent',
  note: 'no curl',
  enabled: true,
  builtin: false,
  ...over,
});

async function boot() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-local-deny-'));
  dirs.push(dir);
  const store = new Store(dir);
  const tokens = {
    'dev-a': store.mintDevice('dev-a', 'dev-a').token,
    'dev-b': store.mintDevice('dev-b', 'dev-b').token,
  };
  store.append(
    { sessionId: SESSION, provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'dev-a' },
    ['{"type":"user"}'],
  );
  const control = createControl(store, {});
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
    const text = await res.text();
    return { status: res.status, body: (text ? JSON.parse(text) : {}) as Msg };
  };
  /** Connects as the device and says hello with `extra`; resolves with what the core answered first. */
  const hello = async (deviceId: 'dev-a' | 'dev-b', extra: Msg): Promise<Msg[]> => {
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
      headers: { authorization: `Bearer ${tokens[deviceId]}` },
    });
    sockets.push(ws);
    const got: Msg[] = [];
    ws.on('message', (data) => got.push(JSON.parse(String(data)) as Msg));
    await new Promise<void>((r) => ws.once('open', () => r()));
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.hello',
        deviceId,
        name: deviceId,
        version: 'test',
        providers: [],
        ...extra,
      }),
    );
    await new Promise((r) => setTimeout(r, 250));
    return got;
  };
  return { store, dir, call, hello, get: (p: string) => call('GET', p) };
}

const local = async (f: Awaited<ReturnType<typeof boot>>, query = '') =>
  (await f.get(`/api/permission-rules${query}`)).body.local as { deviceId: string; rules: Msg[] }[];

describe('permission rules: the deny rules a machine announces', () => {
  it('keeps the announced list and serves it read-only with the rules', async () => {
    const f = await boot();
    expect(await local(f)).toEqual([]);

    await f.hello('dev-a', { permissionDeny: [denyRule('local.a'), denyRule('local.b')] });
    await f.hello('dev-b', { permissionDeny: [denyRule('local.c', { tool: 'read,write' })] });
    // No rule announced is no entry.
    expect(await local(f)).toEqual([
      { deviceId: 'dev-a', rules: [denyRule('local.a'), denyRule('local.b')] },
      { deviceId: 'dev-b', rules: [denyRule('local.c', { tool: 'read,write' })] },
    ]);
    expect(await local(f, '?deviceId=dev-b')).toEqual([
      { deviceId: 'dev-b', rules: [denyRule('local.c', { tool: 'read,write' })] },
    ]);
    expect(((await local(f, `?sessionId=${SESSION}`)) ?? []).map((l) => l.deviceId)).toEqual([
      'dev-a',
    ]);
    expect(await local(f, '?deviceId=nobody')).toEqual([]);

    // The list is not in the rules and the routes cannot change it.
    const all = (await f.get('/api/permission-rules')).body.rules as Msg[];
    expect(all.some((r) => r.scope === 'agent')).toBe(false);
    const put = { scope: 'global', tool: 'run', match: {}, action: 'allow' };
    expect((await f.call('PUT', '/api/permission-rules/local.a', put)).status).toBeGreaterThan(399);
    expect((await f.call('DELETE', '/api/permission-rules/local.a')).status).toBeGreaterThan(399);
    expect((await local(f, '?deviceId=dev-a'))[0]?.rules).toHaveLength(2);
  });

  it('keeps the list across a restart of the core', async () => {
    const f = await boot();
    await f.hello('dev-a', { permissionDeny: [denyRule('local.a')] });
    const reopened = new Store(f.dir);
    expect(reopened.localPermissionDeny('dev-a')).toEqual([
      { deviceId: 'dev-a', rules: [denyRule('local.a')] },
    ]);
  });

  it('replaces the list with the next hello, and a hello without one clears it', async () => {
    const f = await boot();
    await f.hello('dev-a', { permissionDeny: [denyRule('local.a')] });
    await f.hello('dev-a', { permissionDeny: [denyRule('local.z')] });
    expect((await local(f))[0]?.rules.map((r) => r.id)).toEqual(['local.z']);
    await f.hello('dev-a', {});
    expect(await local(f)).toEqual([]);
    await f.hello('dev-a', { permissionDeny: [] });
    expect(await local(f)).toEqual([]);
  });

  it('refuses a hello with too many rules or a rule that is not a local deny rule', async () => {
    const f = await boot();
    const many = Array.from({ length: 101 }, (_, i) => denyRule(`local.${i}`));
    const bad: Msg[][] = [
      many,
      [denyRule('local.a', { action: 'allow' })],
      [denyRule('local.a', { scope: 'global' })],
      [denyRule('local.a', { enabled: false })],
      [denyRule('local.a', { builtin: true })],
      [denyRule('local.a', { extra: 1 })],
    ];
    for (const permissionDeny of bad) {
      const got = await f.hello('dev-a', { permissionDeny });
      expect(got.find((m) => m.type === 'error')?.code).toBe('invalid-message');
    }
    expect(await local(f)).toEqual([]);

    const ok = await f.hello('dev-a', { permissionDeny: many.slice(0, 100) });
    expect(ok.some((m) => m.type === 'error')).toBe(false);
    expect((await local(f))[0]?.rules).toHaveLength(100);
  });
});
