import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  HealthResponseSchema,
  PROTOCOL_VERSION,
  parse,
  WsTicketResponseSchema,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createApi } from '../src/api/index.ts';
import { TICKET_TTL_MS } from '../src/api/tickets.ts';
import { bearerToken, secretsMatch } from '../src/config/index.ts';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

const HOST = 'host-token-0123456789abcdef';
const VERSION_FILE = new URL('../../VERSION', import.meta.url);

const dirs: string[] = [];
const servers: Server[] = [];
const sockets: WebSocket[] = [];

afterEach(async () => {
  for (const ws of sockets.splice(0)) ws.terminate();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

async function boot() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-auth-'));
  dirs.push(dir);
  const store = new Store(dir);
  const control = createControl(store);
  const server = createServer();
  const clock = { now: 1_000_000 };
  attachIngest(server, store, control);
  createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control, {
    now: () => clock.now,
  }).attach(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  servers.push(server);
  const port = (server.address() as AddressInfo).port;
  const rest = (method: string, p: string, token?: string, scheme = 'Bearer') =>
    fetch(`http://127.0.0.1:${port}${p}`, {
      method,
      headers: token === undefined ? {} : { authorization: `${scheme} ${token}` },
    });
  const mintTicket = async () => {
    const res = await rest('POST', '/api/ws-tickets', HOST);
    return parse(WsTicketResponseSchema, await res.json());
  };
  return { store, port, clock, rest, mintTicket };
}

/** Opens a WebSocket and reports how the upgrade ended: 'open', or the HTTP status that refused it. */
function connect(
  url: string,
  headers: Record<string, string> = {},
): Promise<{ outcome: number | 'open'; ws: WebSocket }> {
  return new Promise((resolve) => {
    const ws = new WebSocket(url, { headers });
    sockets.push(ws);
    ws.once('open', () => resolve({ outcome: 'open', ws }));
    ws.once('unexpected-response', (_req, res) => resolve({ outcome: res.statusCode ?? 0, ws }));
    ws.once('error', () => {});
  });
}

describe('REST authentication', () => {
  it('answers 401 for a missing or wrong host token on every /api path', async () => {
    const { rest } = await boot();
    for (const [method, p] of [
      ['GET', '/api/health'],
      ['GET', '/api/sessions'],
      ['POST', '/api/ws-tickets'],
      ['POST', '/api/devices'],
      ['GET', '/api/does-not-exist'],
    ] as const) {
      for (const res of [
        await rest(method, p),
        await rest(method, p, 'wrong'),
        await rest(method, p, HOST.slice(0, -1)),
        await rest(method, p, `${HOST}x`),
        await rest(method, p, HOST, 'Basic'),
      ]) {
        expect(res.status, `${method} ${p}`).toBe(401);
        expect(await res.json()).toEqual({ code: 'unauthorized' });
      }
    }
  });

  it('accepts the host token', async () => {
    const { rest } = await boot();
    expect((await rest('GET', '/api/health', HOST)).status).toBe(200);
  });

  it('reports the core version and the protocol version on /api/health', async () => {
    const { rest } = await boot();
    const res = await rest('GET', '/api/health', HOST);
    expect(res.status).toBe(200);
    const health = parse(HealthResponseSchema, await res.json());
    expect(health.ok).toBe(true);
    expect(health.sessions).toBe(0);
    expect(health.coreVersion).toBe(readFileSync(VERSION_FILE, 'utf8').trim());
    expect(health.protocolVersion).toBe(PROTOCOL_VERSION);
  });

  it('refuses a device token on REST', async () => {
    const { rest, store } = await boot();
    const device = store.mintDevice('agent', 'agent');
    expect((await rest('GET', '/api/sessions', device.token)).status).toBe(401);
  });
});

describe('token comparison', () => {
  it('matches equal secrets only, whatever their lengths', () => {
    expect(secretsMatch(HOST, HOST)).toBe(true);
    expect(secretsMatch(HOST.slice(1), HOST)).toBe(false);
    expect(secretsMatch(`${HOST}x`, HOST)).toBe(false);
    expect(secretsMatch('', HOST)).toBe(false);
    expect(secretsMatch('', '')).toBe(false);
  });

  it('reads only a Bearer credential from the header', () => {
    expect(bearerToken('Bearer abc')).toBe('abc');
    expect(bearerToken('Basic abc')).toBe('');
    expect(bearerToken(undefined)).toBe('');
  });
});

describe('browser WebSocket /ws', () => {
  it('upgrades with a ticket minted by the host token, once', async () => {
    const { port, mintTicket } = await boot();
    const { ticket, expiresInSec } = await mintTicket();
    expect(expiresInSec).toBe(30);
    const url = `ws://127.0.0.1:${port}/ws?ticket=${ticket}`;
    expect((await connect(url)).outcome).toBe('open');
    expect((await connect(url)).outcome).toBe(401);
  });

  it('refuses a ticket after it expires', async () => {
    const { port, clock, mintTicket } = await boot();
    const { ticket } = await mintTicket();
    clock.now += TICKET_TTL_MS + 1;
    expect((await connect(`ws://127.0.0.1:${port}/ws?ticket=${ticket}`)).outcome).toBe(401);
  });

  it('accepts a ticket right up to its expiry', async () => {
    const { port, clock, mintTicket } = await boot();
    const { ticket } = await mintTicket();
    clock.now += TICKET_TTL_MS - 1;
    expect((await connect(`ws://127.0.0.1:${port}/ws?ticket=${ticket}`)).outcome).toBe('open');
  });

  it('refuses no ticket, an invented ticket, and the host token in the URL or the header', async () => {
    const { port } = await boot();
    const base = `ws://127.0.0.1:${port}/ws`;
    expect((await connect(base)).outcome).toBe(401);
    expect((await connect(`${base}?ticket=invented`)).outcome).toBe(401);
    expect((await connect(`${base}?token=${HOST}`)).outcome).toBe(401);
    expect((await connect(`${base}?ticket=${HOST}`)).outcome).toBe(401);
    expect((await connect(base, { authorization: `Bearer ${HOST}` })).outcome).toBe(401);
  });

  it('refuses a device token', async () => {
    const { port, store } = await boot();
    const device = store.mintDevice('agent', 'agent');
    const base = `ws://127.0.0.1:${port}/ws`;
    expect((await connect(`${base}?token=${device.token}`)).outcome).toBe(401);
    expect((await connect(`${base}?ticket=${device.token}`)).outcome).toBe(401);
    expect((await connect(base, { authorization: `Bearer ${device.token}` })).outcome).toBe(401);
  });

  it('mints tickets only for the host token', async () => {
    const { rest } = await boot();
    expect((await rest('POST', '/api/ws-tickets')).status).toBe(401);
    expect((await rest('GET', '/api/ws-tickets', HOST)).status).toBe(405);
  });
});

describe('agent WebSocket /ws/agent', () => {
  it('accepts a device token in the Authorization header', async () => {
    const { port, store } = await boot();
    const device = store.mintDevice('agent', 'agent');
    const { outcome } = await connect(`ws://127.0.0.1:${port}/ws/agent`, {
      authorization: `Bearer ${device.token}`,
    });
    expect(outcome).toBe('open');
  });

  it('refuses the host token, a missing token and a wrong token', async () => {
    const { port } = await boot();
    const url = `ws://127.0.0.1:${port}/ws/agent`;
    expect((await connect(url, { authorization: `Bearer ${HOST}` })).outcome).toBe(401);
    expect((await connect(url)).outcome).toBe(401);
    expect((await connect(url, { authorization: 'Bearer wrong' })).outcome).toBe(401);
  });

  it('refuses a device token that travels in the URL', async () => {
    const { port, store } = await boot();
    const device = store.mintDevice('agent', 'agent');
    const url = `ws://127.0.0.1:${port}/ws/agent`;
    expect((await connect(`${url}?token=${device.token}`)).outcome).toBe(401);
  });

  it('refuses a ticket', async () => {
    const { port, mintTicket } = await boot();
    const { ticket } = await mintTicket();
    expect(
      (await connect(`ws://127.0.0.1:${port}/ws/agent`, { authorization: `Bearer ${ticket}` }))
        .outcome,
    ).toBe(401);
  });

  it('replaces a device token when the device is minted again', async () => {
    const { port, store } = await boot();
    const first = store.mintDevice('agent', 'agent');
    const second = store.mintDevice('agent', 'agent');
    const url = `ws://127.0.0.1:${port}/ws/agent`;
    expect((await connect(url, { authorization: `Bearer ${first.token}` })).outcome).toBe(401);
    expect((await connect(url, { authorization: `Bearer ${second.token}` })).outcome).toBe('open');
  });

  it('answers a hello that names another device with device-mismatch', async () => {
    const { port, store } = await boot();
    store.mintDevice('other', 'other');
    const device = store.mintDevice('agent', 'agent');
    const { ws } = await connect(`ws://127.0.0.1:${port}/ws/agent`, {
      authorization: `Bearer ${device.token}`,
    });
    const messages: Record<string, unknown>[] = [];
    ws.on('message', (raw) => messages.push(JSON.parse(String(raw))));
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.hello',
        deviceId: 'other',
        name: 'imposter',
        version: 't',
      }),
    );
    await new Promise((r) => setTimeout(r, 100));
    expect(messages.at(-1)).toMatchObject({ type: 'error', code: 'device-mismatch' });
    expect(store.listDevices().find((d) => d.deviceId === 'other')?.name).toBe('other');
  });
});
