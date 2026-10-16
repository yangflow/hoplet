import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import type { ServerMessage } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { createApi } from '../src/api/index.ts';
import { createControl } from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';

const HOST = 'host-token-0123456789abcdef';
const dirs: string[] = [];
const servers: Server[] = [];

afterEach(async () => {
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

const line = '{"type":"user","message":{"role":"user","content":"hello"}}';

async function boot() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-actions-'));
  dirs.push(dir);
  const store = new Store(dir);
  for (const id of ['s1', 's2']) {
    store.append({ sessionId: id, projectDir: 'p', filePath: `/${id}`, deviceId: 'dev-a' }, [line]);
  }
  const control = createControl(store);
  const heard: ServerMessage[] = [];
  control.subscribe((m) => heard.push(m));
  const server = createServer();
  createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control).attach(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  servers.push(server);
  const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  const call = async (method: string, p: string, body?: unknown) => {
    const res = await fetch(base + p, {
      method,
      headers: { authorization: `Bearer ${HOST}`, 'content-type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    return { status: res.status, body: (await res.json()) as Record<string, unknown> };
  };
  return { store, heard, call };
}

describe('Store session state', () => {
  it('starts empty, sets each field apart and clears it with null', () => {
    const { store } = (() => {
      const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-actions-'));
      dirs.push(dir);
      const s = new Store(dir);
      s.append({ sessionId: 's1', projectDir: 'p', filePath: '/s1', deviceId: 'd' }, [line]);
      return { store: s };
    })();
    expect(store.sessionStates().size).toBe(0);
    expect(store.updateSessionState('s1', { titleOverride: 'Mine' })).toEqual({
      titleOverride: 'Mine',
    });
    expect(store.updateSessionState('s1', { pinnedAt: 5, archivedAt: 6 })).toEqual({
      titleOverride: 'Mine',
      pinnedAt: 5,
      archivedAt: 6,
    });
    expect(store.updateSessionState('s1', { pinnedAt: null })).toEqual({
      titleOverride: 'Mine',
      archivedAt: 6,
    });
    expect(store.sessionStates().get('s1')).toEqual({ titleOverride: 'Mine', archivedAt: 6 });
    expect(store.updateSessionState('s1', { titleOverride: null, archivedAt: null })).toEqual({});
    expect(store.sessionStates().size).toBe(0);
    expect(store.updateSessionState('missing', { pinnedAt: 1 })).toBeNull();
  });

  it('leaves the lines, the cursor and the vendor title alone', () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-actions-'));
    dirs.push(dir);
    const store = new Store(dir);
    store.append({ sessionId: 's1', projectDir: 'p', filePath: '/s1', deviceId: 'd' }, [line]);
    const before = store.getSession('s1');
    store.updateSessionState('s1', { titleOverride: 'Mine', pinnedAt: 1, archivedAt: 2 });
    const after = store.getSession('s1');
    expect(after?.title).toBe(before?.title);
    expect(after?.byteSize).toBe(before?.byteSize);
    expect(after?.lineCount).toBe(before?.lineCount);
  });
});

describe('PATCH /api/sessions/:sessionId', () => {
  it('pins, renames and archives, answers with the session and tells the browsers', async () => {
    const { call, heard } = await boot();
    const renamed = await call('PATCH', '/api/sessions/s1', { title: '  Mine  ', pinned: true });
    expect(renamed.status).toBe(200);
    expect(renamed.body).toMatchObject({ sessionId: 's1', titleOverride: 'Mine' });
    expect(typeof renamed.body.pinnedAt).toBe('number');
    expect(renamed.body.archivedAt).toBeUndefined();
    expect(heard).toContainEqual(
      expect.objectContaining({ type: 'session.updated', sessionId: 's1', titleOverride: 'Mine' }),
    );

    const archived = await call('PATCH', '/api/sessions/s1', { archived: true });
    expect(typeof archived.body.archivedAt).toBe('number');
    expect(archived.body.titleOverride).toBe('Mine');

    const list = await call('GET', '/api/sessions');
    const s1 = (list.body.sessions as Record<string, unknown>[]).find((s) => s.sessionId === 's1');
    expect(s1?.archivedAt).toBeDefined();
    expect((list.body.sessions as unknown[]).length).toBe(2);

    const cleared = await call('PATCH', '/api/sessions/s1', {
      title: null,
      pinned: false,
      archived: false,
    });
    expect(cleared.body.titleOverride).toBeUndefined();
    expect(cleared.body.pinnedAt).toBeUndefined();
    expect(cleared.body.archivedAt).toBeUndefined();
    const last = heard.at(-1);
    expect(last).toMatchObject({ type: 'session.updated', sessionId: 's1' });
    expect(last).not.toHaveProperty('pinnedAt');
  });

  it('stores a blank title as no override', async () => {
    const { call } = await boot();
    await call('PATCH', '/api/sessions/s1', { title: 'Mine' });
    const blank = await call('PATCH', '/api/sessions/s1', { title: '   ' });
    expect(blank.body.titleOverride).toBeUndefined();
  });

  it('stores no title when the trimmed one is the vendor title', async () => {
    const { call, store } = await boot();
    const vendor = store.getSession('s1')?.title ?? '';
    await call('PATCH', '/api/sessions/s1', { title: 'Mine' });
    const same = await call('PATCH', '/api/sessions/s1', { title: `  ${vendor} ` });
    expect(same.body.titleOverride).toBeUndefined();
    expect(store.sessionState('s1')).toEqual({});
  });

  it('counts the length of the trimmed title', async () => {
    const { call } = await boot();
    const padded = ` ${'x'.repeat(200)} `;
    const ok = await call('PATCH', '/api/sessions/s1', { title: padded });
    expect(ok.status).toBe(200);
    expect(ok.body.titleOverride).toBe('x'.repeat(200));
    const long = await call('PATCH', '/api/sessions/s1', { title: ` ${'x'.repeat(201)} ` });
    expect(long.status).toBe(400);
  });

  it('keeps the time of a pin or an archive that is set again', async () => {
    const { call } = await boot();
    const first = await call('PATCH', '/api/sessions/s1', { pinned: true, archived: true });
    await new Promise((r) => setTimeout(r, 5));
    const again = await call('PATCH', '/api/sessions/s1', { pinned: true, archived: true });
    expect(again.body.pinnedAt).toBe(first.body.pinnedAt);
    expect(again.body.archivedAt).toBe(first.body.archivedAt);
    await call('PATCH', '/api/sessions/s1', { pinned: false });
    await new Promise((r) => setTimeout(r, 5));
    const repinned = await call('PATCH', '/api/sessions/s1', { pinned: true });
    expect(Number(repinned.body.pinnedAt)).toBeGreaterThan(Number(first.body.pinnedAt));
  });

  it('refuses an empty change, a bad body and an unknown session', async () => {
    const { call, heard } = await boot();
    expect((await call('PATCH', '/api/sessions/s1', {})).body).toMatchObject({
      code: 'invalid-request',
    });
    expect((await call('PATCH', '/api/sessions/s1', { pinned: 'yes' })).status).toBe(400);
    expect((await call('PATCH', '/api/sessions/s1', { title: 'x'.repeat(201) })).status).toBe(400);
    const missing = await call('PATCH', '/api/sessions/nope', { pinned: true });
    expect(missing.status).toBe(404);
    expect(missing.body).toEqual({ code: 'session-not-found' });
    expect(heard.filter((m) => m.type === 'session.updated')).toEqual([]);
  });
});
