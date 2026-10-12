import { mkdtempSync, rmSync } from 'node:fs';
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  ErrorResponseSchema,
  type HttpErrorCode,
  LiveSessionMetaSchema,
  PROTOCOL_VERSION,
  safeParse,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createApi } from '../src/api/index.ts';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

const cleanups: (() => void)[] = [];
afterEach(() => {
  for (const cleanup of cleanups.splice(0)) cleanup();
});

async function boot() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-validation-'));
  const store = new Store(dir);
  const server = createServer();
  const config = { port: 0, dataDir: dir, token: 'host-token', webDir: null };
  const control = createControl(store);
  attachIngest(server, store, control);
  createApi(store, config, control).attach(server);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = (server.address() as AddressInfo).port;
  cleanups.push(() => {
    server.close();
    server.closeAllConnections();
    rmSync(dir, { force: true, recursive: true });
  });
  const call = (method: string, url: string, body?: string, token: string | null = 'host-token') =>
    fetch(`http://127.0.0.1:${port}${url}`, {
      method,
      headers: {
        ...(token ? { authorization: `Bearer ${token}` } : {}),
        'content-type': 'application/json',
      },
      ...(body === undefined ? {} : { body }),
    });
  return { store, port, call };
}

async function expectError(res: Response, status: number, code: HttpErrorCode) {
  expect(res.status).toBe(status);
  const body = await res.json();
  expect(safeParse(ErrorResponseSchema, body).success).toBe(true);
  expect(body).toMatchObject({ code });
}

describe('REST errors carry a stable code', () => {
  it('refuses a missing token, an unknown path and an unknown session', async () => {
    const { call } = await boot();
    await expectError(await call('GET', '/api/sessions', undefined, null), 401, 'unauthorized');
    await expectError(await call('GET', '/api/nothing'), 404, 'not-found');
    await expectError(await call('GET', '/api/sessions/absent'), 404, 'session-not-found');
    await expectError(await call('GET', '/api/sessions/absent/lines'), 404, 'session-not-found');
    await expectError(
      await call('POST', '/api/sessions/absent/prompt', '{"text":"x","promptId":"p"}'),
      404,
      'session-not-found',
    );
  });

  it('refuses bodies that are not JSON, do not match the schema, or are too large', async () => {
    const { call } = await boot();
    await expectError(await call('POST', '/api/devices', '{'), 400, 'invalid-json');
    await expectError(await call('POST', '/api/devices', '{}'), 400, 'invalid-request');
    await expectError(await call('POST', '/api/devices', '{"name":"  "}'), 400, 'invalid-request');
    await expectError(await call('POST', '/api/devices', '{"name":7}'), 400, 'invalid-request');
    await expectError(await call('POST', '/api/devices', '[]'), 400, 'invalid-request');
    await expectError(
      await call('POST', '/api/devices', JSON.stringify({ name: 'x'.repeat(5000) })),
      413,
      'too-large',
    );
    await expectError(
      await call('POST', '/api/devices/d1/sessions/import', '{"path":"/p","sessionIds":["a/b"]}'),
      400,
      'invalid-request',
    );
    await expectError(
      await call('POST', '/api/devices/d1/sessions/import', '{"path":"/p","sessionIds":[]}'),
      400,
      'invalid-request',
    );
    await expectError(
      await call('POST', '/api/devices/d1/projects', '{"path":""}'),
      400,
      'invalid-request',
    );
  });

  it('refuses queries that do not match the schema and clamps numbers that are only out of range', async () => {
    const { store, call } = await boot();
    await expectError(await call('GET', '/api/search'), 400, 'invalid-request');
    await expectError(await call('GET', '/api/search?q=%20'), 400, 'invalid-request');
    await expectError(await call('GET', '/api/sessions/s1/search'), 400, 'invalid-request');
    await expectError(await call('GET', '/api/sessions/s1/workspace/file'), 400, 'invalid-request');
    store.append({ sessionId: 's1', projectDir: 'p', filePath: '/x', deviceId: 'd' }, ['{}', '{}']);
    const page = await (await call('GET', '/api/sessions/s1/lines?start=abc&limit=zzz')).json();
    expect(page).toMatchObject({ sessionId: 's1', total: 2, start: 0, end: 2 });
  });

  it('returns the provider of a session and validates against the session schema', async () => {
    const { store, call } = await boot();
    store.append({ sessionId: 's1', projectDir: 'p', filePath: '/x', deviceId: 'd' }, ['{}']);
    const list = await (await call('GET', '/api/sessions')).json();
    expect(list.sessions).toHaveLength(1);
    expect(safeParse(LiveSessionMetaSchema, list.sessions[0]).success).toBe(true);
    expect(list.sessions[0]).toMatchObject({ provider: 'claude', ownerConnected: false });
    const one = await (await call('GET', '/api/sessions/s1')).json();
    expect(one.provider).toBe('claude');
  });
});

describe('agent WebSocket validates every message', () => {
  async function connect() {
    const { store, port } = await boot();
    const device = store.mintDevice('agent', 'agent');
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
      headers: { authorization: `Bearer ${device.token}` },
    });
    cleanups.push(() => ws.terminate());
    const messages: Record<string, unknown>[] = [];
    ws.on('message', (raw) => messages.push(JSON.parse(String(raw))));
    const closed = new Promise<number>((resolve) => ws.on('close', (code) => resolve(code)));
    await new Promise<void>((resolve, reject) => {
      ws.once('open', () => resolve());
      ws.once('error', reject);
    });
    const next = async () => {
      for (let i = 0; i < 100 && messages.length === 0; i++) {
        await new Promise((resolve) => setTimeout(resolve, 10));
      }
      const first = messages.shift();
      if (!first) throw new Error('no message');
      return first;
    };
    await next(); // welcome
    return { ws, next, closed, messages };
  }

  it('answers every malformed frame with a code and keeps the connection', async () => {
    const { ws, next } = await connect();
    const cases: [string, string][] = [
      ['not json', 'invalid-json'],
      ['[]', 'invalid-message'],
      ['null', 'invalid-message'],
      [JSON.stringify({ v: PROTOCOL_VERSION }), 'invalid-message'],
      [JSON.stringify({ type: 'ping' }), 'invalid-message'],
      [JSON.stringify({ v: PROTOCOL_VERSION, type: 'nope' }), 'unknown-type'],
      [JSON.stringify({ v: PROTOCOL_VERSION, type: 'welcome' }), 'unknown-type'],
      [
        JSON.stringify({ v: PROTOCOL_VERSION, type: 'device.hello', deviceId: 'agent' }),
        'invalid-message',
      ],
      [
        JSON.stringify({ v: PROTOCOL_VERSION, type: 'session.append', sessionId: 'a/b' }),
        'invalid-session-id',
      ],
      [
        JSON.stringify({ v: PROTOCOL_VERSION, type: 'session.append', sessionId: 'ok' }),
        'invalid-message',
      ],
      [
        JSON.stringify({
          v: PROTOCOL_VERSION,
          type: 'session.append',
          sessionId: 'ok',
          projectDir: 'p',
          filePath: '/x',
          byteStart: -1,
          encoding: 'plain',
          lines: '',
        }),
        'invalid-message',
      ],
    ];
    for (const [frame, code] of cases) {
      ws.send(frame);
      expect(await next(), frame).toMatchObject({ type: 'error', code });
    }
    ws.send(JSON.stringify({ v: PROTOCOL_VERSION, type: 'ping', t: 5 }));
    expect(await next()).toMatchObject({ type: 'pong', t: 5 });
  });

  it('refuses a device.hello with another protocol version and closes the connection', async () => {
    const { ws, next, closed } = await connect();
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION + 1,
        type: 'device.hello',
        deviceId: 'agent',
        name: 'agent',
        version: 'test',
      }),
    );
    expect(await next()).toMatchObject({ type: 'error', code: 'protocol-version-mismatch' });
    expect(await closed).toBe(1008);
  });

  it('refuses any other message with another protocol version', async () => {
    const { ws, next } = await connect();
    ws.send(JSON.stringify({ v: PROTOCOL_VERSION - 1, type: 'ping' }));
    expect(await next()).toMatchObject({ type: 'error', code: 'protocol-version-mismatch' });
  });

  it('accepts a device.hello with the current protocol version', async () => {
    const { ws, next, messages } = await connect();
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.hello',
        deviceId: 'agent',
        name: 'agent',
        version: 'test',
      }),
    );
    ws.send(JSON.stringify({ v: PROTOCOL_VERSION, type: 'ping' }));
    expect(await next()).toMatchObject({ type: 'pong' });
    expect(messages).toEqual([]);
  });
});
