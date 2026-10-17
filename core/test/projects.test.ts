import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createApi } from '../src/api/index.ts';
import type { Config } from '../src/config/index.ts';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
const servers: Server[] = [];
const sockets: WebSocket[] = [];

afterEach(async () => {
  for (const ws of sockets.splice(0)) {
    ws.terminate();
  }
  for (const s of servers.splice(0)) {
    await new Promise<void>((resolve) => s.close(() => resolve()));
  }
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

async function boot(): Promise<{ port: number; store: Store; token: string }> {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-projects-'));
  dirs.push(dir);
  const store = new Store(dir);
  const token = 'tok';
  const config: Config = { port: 0, dataDir: dir, token, webDir: null };
  const server = createServer();
  const control = createControl(store, { workspaceTimeoutMs: 400 });
  attachIngest(server, store, control);
  createApi(store, config, control).attach(server);
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  servers.push(server);
  const port = (server.address() as AddressInfo).port;
  return { port, store, token };
}

function openWs(
  port: number,
  token: string,
): Promise<{ ws: WebSocket; welcome: Record<string, unknown> }> {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
      headers: { authorization: `Bearer ${token}` },
    });
    sockets.push(ws);
    const onErr = (err: Error) => reject(err);
    ws.once('error', onErr);
    ws.once('message', (data) => {
      ws.off('error', onErr);
      resolve({ ws, welcome: JSON.parse(String(data)) as Record<string, unknown> });
    });
  });
}

describe('GET /api/devices/:id/projects', () => {
  it('returns 409 when the agent is offline', async () => {
    const { port, token, store } = await boot();
    store.mintDevice('studio', 'dev-a');
    const res = await fetch(`http://127.0.0.1:${port}/api/devices/dev-a/projects`, {
      headers: { authorization: `Bearer ${token}` },
    });
    expect(res.status).toBe(409);
  });

  it('forwards list add sessions import and remove to the live agent', async () => {
    const { port, token, store } = await boot();
    const minted = store.mintDevice('studio', 'dev-a');
    const { ws } = await openWs(port, minted.token);
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.hello',
        deviceId: 'dev-a',
        name: 'studio',
        version: 'test',
      }),
    );
    await new Promise((r) => setTimeout(r, 40));
    const inbox: Record<string, unknown>[] = [];
    ws.on('message', (d) => inbox.push(JSON.parse(String(d)) as Record<string, unknown>));

    const pending = fetch(`http://127.0.0.1:${port}/api/devices/dev-a/projects`, {
      headers: { authorization: `Bearer ${token}` },
    });
    let listMsg: Record<string, unknown> | undefined;
    for (let i = 0; i < 50; i++) {
      listMsg = inbox.find((m) => m.type === 'device.projects.list');
      if (listMsg) break;
      await new Promise((r) => setTimeout(r, 20));
    }
    expect(listMsg).toMatchObject({ type: 'device.projects.list' });
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.projects',
        requestId: listMsg?.requestId,
        candidates: ['/tmp/app'],
        projects: [],
      }),
    );
    const listed = await pending;
    expect(listed.status).toBe(200);
    expect(await listed.json()).toMatchObject({
      deviceId: 'dev-a',
      candidates: ['/tmp/app'],
      projects: [],
    });

    const addPending = fetch(`http://127.0.0.1:${port}/api/devices/dev-a/projects`, {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: JSON.stringify({ path: '/tmp/app' }),
    });
    let add: Record<string, unknown> | undefined;
    for (let i = 0; i < 50; i++) {
      add = inbox.find((m) => m.type === 'device.projects.add');
      if (add) break;
      await new Promise((r) => setTimeout(r, 20));
    }
    expect(add).toMatchObject({ type: 'device.projects.add', path: '/tmp/app' });
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.projects',
        requestId: add?.requestId,
        candidates: ['/tmp/app'],
        projects: [
          {
            path: '/tmp/app',
            sessions: [{ sessionId: 'keep', cwd: '/tmp/app', imported: false }],
          },
        ],
      }),
    );
    const added = await addPending;
    expect(added.status).toBe(200);

    const importPending = fetch(`http://127.0.0.1:${port}/api/devices/dev-a/sessions/import`, {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: JSON.stringify({ path: '/tmp/app', sessionIds: ['keep'] }),
    });
    let imp: Record<string, unknown> | undefined;
    for (let i = 0; i < 50; i++) {
      imp = inbox.find((m) => m.type === 'device.sessions.import');
      if (imp) break;
      await new Promise((r) => setTimeout(r, 20));
    }
    expect(imp).toMatchObject({
      type: 'device.sessions.import',
      path: '/tmp/app',
      sessionIds: ['keep'],
    });
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.projects',
        requestId: imp?.requestId,
        candidates: ['/tmp/app'],
        projects: [
          {
            path: '/tmp/app',
            sessions: [{ sessionId: 'keep', cwd: '/tmp/app', imported: true }],
          },
        ],
      }),
    );
    const imported = await importPending;
    expect(imported.status).toBe(200);
    expect(await imported.json()).toMatchObject({
      projects: [{ sessions: [{ sessionId: 'keep', imported: true }] }],
    });

    const removePending = fetch(`http://127.0.0.1:${port}/api/devices/dev-a/sessions/remove`, {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: JSON.stringify({ path: '/tmp/app', sessionIds: ['keep'] }),
    });
    let rem: Record<string, unknown> | undefined;
    for (let i = 0; i < 50; i++) {
      rem = inbox.find((m) => m.type === 'device.sessions.remove');
      if (rem) break;
      await new Promise((r) => setTimeout(r, 20));
    }
    expect(rem).toMatchObject({
      type: 'device.sessions.remove',
      path: '/tmp/app',
      sessionIds: ['keep'],
    });
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.projects',
        requestId: rem?.requestId,
        candidates: ['/tmp/app'],
        projects: [
          {
            path: '/tmp/app',
            sessions: [{ sessionId: 'keep', cwd: '/tmp/app', imported: false }],
          },
        ],
      }),
    );
    const removed = await removePending;
    expect(removed.status).toBe(200);
    expect(await removed.json()).toMatchObject({
      projects: [{ sessions: [{ sessionId: 'keep', imported: false }] }],
    });

    const pathRemovePending = fetch(`http://127.0.0.1:${port}/api/devices/dev-a/projects/remove`, {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: JSON.stringify({ path: '/tmp/app' }),
    });
    let pathRem: Record<string, unknown> | undefined;
    for (let i = 0; i < 50; i++) {
      pathRem = inbox.find((m) => m.type === 'device.projects.remove');
      if (pathRem) break;
      await new Promise((r) => setTimeout(r, 20));
    }
    expect(pathRem).toMatchObject({ type: 'device.projects.remove', path: '/tmp/app' });
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.projects',
        requestId: pathRem?.requestId,
        candidates: [],
        projects: [],
      }),
    );
    const pathRemoved = await pathRemovePending;
    expect(pathRemoved.status).toBe(200);
    expect(await pathRemoved.json()).toMatchObject({
      deviceId: 'dev-a',
      candidates: [],
      projects: [],
    });
  });
});

describe('GET /api/devices/:id/dirs', () => {
  async function connect() {
    const { port, token, store } = await boot();
    const minted = store.mintDevice('studio', 'dev-a');
    const { ws } = await openWs(port, minted.token);
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.hello',
        deviceId: 'dev-a',
        name: 'studio',
        version: 'test',
      }),
    );
    await new Promise((r) => setTimeout(r, 40));
    const inbox: Record<string, unknown>[] = [];
    ws.on('message', (d) => inbox.push(JSON.parse(String(d)) as Record<string, unknown>));
    const get = (query: string, id = 'dev-a') =>
      fetch(`http://127.0.0.1:${port}/api/devices/${id}/dirs${query}`, {
        headers: { authorization: `Bearer ${token}` },
      });
    const request = async (): Promise<Record<string, unknown>> => {
      for (let i = 0; i < 50; i++) {
        const msg = inbox.find((m) => m.type === 'device.dirs.list');
        if (msg) return msg;
        await new Promise((r) => setTimeout(r, 20));
      }
      throw new Error('no device.dirs.list arrived');
    };
    return { ws, get, request };
  }

  it('refuses an offline device with target-offline', async () => {
    const { port, token, store } = await boot();
    store.mintDevice('studio', 'dev-a');
    const res = await fetch(`http://127.0.0.1:${port}/api/devices/dev-a/dirs`, {
      headers: { authorization: `Bearer ${token}` },
    });
    expect(res.status).toBe(409);
    expect(await res.json()).toEqual({ code: 'target-offline' });
  });

  it('rejects an empty path before anything is forwarded', async () => {
    const { get } = await connect();
    const res = await get('?path=%20');
    expect(res.status).toBe(400);
    expect(await res.json()).toMatchObject({ code: 'invalid-request' });
  });

  it('asks for the browse root without a path and relays the listing', async () => {
    const { ws, get, request } = await connect();
    const pending = get('');
    const msg = await request();
    expect(msg).toMatchObject({ v: PROTOCOL_VERSION, type: 'device.dirs.list', path: '~' });
    const listing = {
      path: '/home/me',
      parent: null,
      root: '/home/me',
      entries: [{ name: 'app', git: true }],
      truncated: false,
    };
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.dirs',
        requestId: msg.requestId,
        ...listing,
      }),
    );
    const res = await pending;
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual(listing);
  });

  it('forwards the requested path', async () => {
    const { ws, get, request } = await connect();
    const pending = get('?path=%2Fhome%2Fme%2Fapp');
    const msg = await request();
    expect(msg).toMatchObject({ path: '/home/me/app' });
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.dirs',
        requestId: msg.requestId,
        path: '/home/me/app',
        parent: '/home/me',
        root: '/home/me',
        entries: [],
        truncated: true,
      }),
    );
    expect(await (await pending).json()).toMatchObject({ parent: '/home/me', truncated: true });
  });

  it('answers a rejection with invalid-request and the reason', async () => {
    const { ws, get, request } = await connect();
    const pending = get('?path=..%2Fx');
    const msg = await request();
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.dirs.rejected',
        requestId: msg.requestId,
        reason: 'relative path',
      }),
    );
    const res = await pending;
    expect(res.status).toBe(400);
    expect(await res.json()).toEqual({ code: 'invalid-request', reason: 'relative path' });
  });

  it('answers timeout when the agent stays silent', async () => {
    const { get } = await connect();
    const res = await get('');
    expect(res.status).toBe(504);
    expect(await res.json()).toEqual({ code: 'timeout' });
  });
});

describe('GET /api/devices/:id/clone', () => {
  async function connect() {
    const { port, token, store } = await boot();
    const minted = store.mintDevice('studio', 'dev-a');
    const { ws } = await openWs(port, minted.token);
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.hello',
        deviceId: 'dev-a',
        name: 'studio',
        version: 'test',
      }),
    );
    await new Promise((r) => setTimeout(r, 40));
    const inbox: Record<string, unknown>[] = [];
    ws.on('message', (d) => inbox.push(JSON.parse(String(d)) as Record<string, unknown>));
    const get = (query: string, id = 'dev-a') =>
      fetch(`http://127.0.0.1:${port}/api/devices/${id}/clone${query}`, {
        headers: { authorization: `Bearer ${token}` },
      });
    const request = async (type = 'device.clone.find'): Promise<Record<string, unknown>> => {
      for (let i = 0; i < 50; i++) {
        const msg = inbox.find((m) => m.type === type);
        if (msg) return msg;
        await new Promise((r) => setTimeout(r, 20));
      }
      throw new Error(`no ${type} arrived`);
    };
    return { ws, get, request, store, port, token };
  }

  it('refuses an offline device with target-offline', async () => {
    const { port, token, store } = await boot();
    store.mintDevice('studio', 'dev-a');
    const res = await fetch(`http://127.0.0.1:${port}/api/devices/dev-a/clone?origin=x`, {
      headers: { authorization: `Bearer ${token}` },
    });
    expect(res.status).toBe(409);
    expect(await res.json()).toEqual({ code: 'target-offline' });
  });

  it('needs an origin', async () => {
    const { get } = await connect();
    const res = await get('');
    expect(res.status).toBe(400);
    expect(await res.json()).toMatchObject({ code: 'invalid-request' });
  });

  it('forwards the origin and relays a clone it found, without a clone object', async () => {
    const { ws, get, request } = await connect();
    const pending = get('?origin=git%40github.com%3Ao%2Fhoplet.git');
    const msg = await request();
    expect(msg).toMatchObject({
      v: PROTOCOL_VERSION,
      type: 'device.clone.find',
      origin: 'git@github.com:o/hoplet.git',
    });
    ws.send(
      JSON.stringify({
        v: PROTOCOL_VERSION,
        type: 'device.clone',
        requestId: msg.requestId,
        path: '/home/me/hoplet',
      }),
    );
    const res = await pending;
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ deviceId: 'dev-a', path: '/home/me/hoplet' });
  });

  describe('with no clone', () => {
    /** Answers the lookup with null and the check with `checked`; returns the check message and the answer. */
    async function lookup(query: string, checked: Record<string, unknown>) {
      const c = await connect();
      const pending = c.get(query);
      const find = await c.request();
      c.ws.send(
        JSON.stringify({
          v: PROTOCOL_VERSION,
          type: 'device.clone',
          requestId: find.requestId,
          path: null,
        }),
      );
      const check = await c.request('device.clone.check');
      c.ws.send(
        JSON.stringify({
          v: PROTOCOL_VERSION,
          type: 'device.clone.checked',
          requestId: check.requestId,
          ...checked,
        }),
      );
      const res = await pending;
      return { check, res, body: (await res.json()) as Record<string, unknown> };
    }

    it('answers path null and where and whether it can be cloned', async () => {
      const { check, res, body } = await lookup('?origin=git%40github.com%3Ao%2Fhoplet.git', {
        reason: 'ok',
        cloneInto: '/home/me/work/hoplet',
      });
      expect(check).toMatchObject({ origin: 'git@github.com:o/hoplet.git' });
      expect(check.cloneInto).toBeUndefined();
      expect(res.status).toBe(200);
      expect(body).toEqual({
        deviceId: 'dev-a',
        path: null,
        clone: {
          origin: 'git@github.com:o/hoplet.git',
          repoName: 'hoplet',
          cloneInto: '/home/me/work/hoplet',
          reason: 'ok',
        },
      });
    });

    it('passes the chosen cloneInto on to the check', async () => {
      const { check, body } = await lookup(
        '?origin=https%3A%2F%2Fgithub.com%2Fo%2Fhoplet&cloneInto=%2Fhome%2Fme%2Fx',
        { reason: 'destination-taken', detail: 'exists', cloneInto: '/home/me/x' },
      );
      expect(check).toMatchObject({ cloneInto: '/home/me/x' });
      expect(body.clone).toEqual({
        origin: 'https://github.com/o/hoplet',
        repoName: 'hoplet',
        cloneInto: '/home/me/x',
        reason: 'destination-taken',
        detail: 'exists',
      });
    });

    it.each(['no-origin', 'no-access'])('relays the reason %s', async (reason) => {
      const { body } = await lookup('?origin=https%3A%2F%2Fgithub.com%2Fo%2Fhoplet.git', {
        reason,
        cloneInto: '/home/me/hoplet',
      });
      expect(body.clone).toMatchObject({ reason, repoName: 'hoplet' });
    });

    it('removes credentials from the origin it answers', async () => {
      const { body } = await lookup(
        '?origin=https%3A%2F%2Fme%3Asecret-token%40github.com%2Fo%2Fhoplet.git',
        { reason: 'no-origin', cloneInto: '/home/me/hoplet' },
      );
      expect(JSON.stringify(body)).not.toContain('secret-token');
      expect(body.clone).toMatchObject({ origin: 'https://github.com/o/hoplet.git' });
    });
  });
});

describe('POST /api/sessions/:id/handoff with cloneInto', () => {
  async function api() {
    const { port, store, token } = await boot();
    store.append({ sessionId: 's1', projectDir: 'p', filePath: '/x', deviceId: 'dev-a' }, [
      '{"n":1}',
    ]);
    const call = (path: string, body?: unknown) =>
      fetch(`http://127.0.0.1:${port}${path}`, {
        method: 'POST',
        headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
    return { call, store };
  }

  it('refuses targetPath together with cloneInto', async () => {
    const { call } = await api();
    const res = await call('/api/sessions/s1/handoff', {
      targetDeviceId: 'dev-b',
      targetPath: '/a',
      cloneInto: '/b',
    });
    expect(res.status).toBe(400);
    expect(await res.json()).toMatchObject({ code: 'invalid-request' });
  });

  it('cancel answers handoff-not-running (409) when nothing is cloning', async () => {
    const { call } = await api();
    const res = await call('/api/sessions/s1/handoff/cancel');
    expect(res.status).toBe(409);
    expect(await res.json()).toEqual({ code: 'handoff-not-running' });
  });
});
