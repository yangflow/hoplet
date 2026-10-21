import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { MAX_TEMPLATES, PROTOCOL_VERSION } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createApi } from '../src/api/index.ts';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { freeName, Store } from '../src/store/index.ts';

const HOST = 'host-token-0123456789abcdef';
const CONFIGURATION = {
  id: 'claude',
  name: 'Claude Code',
  extends: 'claude',
  enabled: true,
  env: [],
  defaults: {},
};
const PROVIDERS = [
  {
    provider: 'claude',
    configuration: CONFIGURATION,
    capabilities: { permissions: true, interrupt: true, config: [] },
  },
];
const PROJECT = '/work/app';
const NEW_ID = '3b1f1b0e-3c1a-4d6e-9a53-0c6f0f3f2a11';

const dirs: string[] = [];
const servers: Server[] = [];
const sockets: WebSocket[] = [];

afterEach(async () => {
  for (const ws of sockets.splice(0)) ws.terminate();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

type Msg = Record<string, unknown>;

async function boot() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-templates-'));
  dirs.push(dir);
  const store = new Store(dir);
  const control = createControl(store, { startTimeoutMs: 400 });
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
  /** A connected agent that watches `PROJECT`; the messages it received are in `got`. */
  const agent = async (deviceId: string) => {
    const { token } = store.mintDevice(deviceId, deviceId);
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
      headers: { authorization: `Bearer ${token}` },
    });
    sockets.push(ws);
    const got: Msg[] = [];
    const send = (msg: Msg) => ws.send(JSON.stringify({ v: PROTOCOL_VERSION, ...msg }));
    ws.on('message', (data) => {
      const m = JSON.parse(String(data)) as Msg;
      got.push(m);
      if (m.type === 'device.projects.list') {
        send({
          type: 'device.projects',
          requestId: m.requestId,
          candidates: [],
          projects: [{ path: PROJECT, sessions: [] }],
        });
      }
    });
    await new Promise<void>((r) => ws.once('open', () => r()));
    send({ type: 'device.hello', deviceId, name: deviceId, version: 'test', providers: PROVIDERS });
    for (let i = 0; i < 100 && !got.some((m) => m.type === 'welcome'); i++) {
      await new Promise((r) => setTimeout(r, 20));
    }
    const starts = () => got.filter((m) => m.type === 'runtime.start');
    const nextStart = async () => {
      for (let i = 0; i < 100 && starts().length === 0; i++) {
        await new Promise((r) => setTimeout(r, 20));
      }
      return starts()[0] as Msg;
    };
    return { send, starts, nextStart };
  };
  return {
    store,
    agent,
    get: (p: string) => call('GET', p),
    post: (p: string, b?: unknown) => call('POST', p, b),
    put: (p: string, b?: unknown) => call('PUT', p, b),
    del: (p: string) => call('DELETE', p),
    raw: async (p: string) =>
      fetch(`http://127.0.0.1:${port}${p}`, { method: 'GET' }).then((r) => r.status),
  };
}

const body = (over: Msg = {}) => ({
  name: 'Fix an issue',
  text: 'Fix {{issue}} in {{path:src/}}',
  provider: 'claude',
  ...over,
});

const created = async (f: Awaited<ReturnType<typeof boot>>, over: Msg = {}) => {
  const res = await f.post('/api/templates', body(over));
  expect(res.status).toBe(201);
  return res.body;
};

describe('templates: the store routes', () => {
  it('creates, reads, lists, updates and deletes', async () => {
    const f = await boot();
    const t = await created(f, {
      deviceId: 'dev-a',
      projectPath: PROJECT,
      configuration: 'claude',
    });
    expect(t).toMatchObject({
      name: 'Fix an issue',
      deviceId: 'dev-a',
      projectPath: PROJECT,
      provider: 'claude',
      configuration: 'claude',
      placeholders: [{ name: 'issue' }, { name: 'path', default: 'src/' }],
    });
    expect(typeof t.id).toBe('string');
    expect((await f.get(`/api/templates/${t.id}`)).body).toEqual(t);
    expect((await f.get('/api/templates')).body).toEqual({ templates: [t] });

    const put = await f.put(
      `/api/templates/${t.id}`,
      body({ name: 'Renamed', text: 'Just {{x}}' }),
    );
    expect(put.status).toBe(200);
    expect(put.body).toMatchObject({ id: t.id, name: 'Renamed', placeholders: [{ name: 'x' }] });
    expect(put.body.deviceId).toBeUndefined();

    expect((await f.del(`/api/templates/${t.id}`)).body).toEqual({ templates: [] });
    expect((await f.get(`/api/templates/${t.id}`)).status).toBe(404);
    expect((await f.del(`/api/templates/${t.id}`)).status).toBe(404);
    expect((await f.put(`/api/templates/${t.id}`, body())).status).toBe(404);
  });

  it('wants the host token', async () => {
    const f = await boot();
    expect(await f.raw('/api/templates')).toBe(401);
    expect(await f.raw('/api/templates/export')).toBe(401);
  });

  it('keeps the name between 1 and 80 characters and the text within 8000', async () => {
    const f = await boot();
    for (const bad of [
      body({ name: '' }),
      body({ name: '   ' }),
      body({ name: 'n'.repeat(81) }),
      body({ text: 'x'.repeat(8001) }),
      body({ provider: 'nope' }),
    ]) {
      expect((await f.post('/api/templates', bad)).status, JSON.stringify(bad).slice(0, 60)).toBe(
        400,
      );
    }
    expect((await f.post('/api/templates', body({ name: 'n'.repeat(80) }))).status).toBe(201);
    expect(
      (await f.post('/api/templates', body({ name: 'long', text: 'x'.repeat(8000) }))).status,
    ).toBe(201);
  });

  it('keeps names unique', async () => {
    const f = await boot();
    const a = await created(f);
    expect(await f.post('/api/templates', body())).toEqual({
      status: 400,
      body: { code: 'invalid-request', reason: 'name' },
    });
    const b = await created(f, { name: 'Other' });
    expect((await f.put(`/api/templates/${b.id}`, body())).body).toEqual({
      code: 'invalid-request',
      reason: 'name',
    });
    // Saving a template under its own name is not a clash.
    expect((await f.put(`/api/templates/${a.id}`, body({ text: 'again' }))).status).toBe(200);
  });

  it('refuses a text whose placeholders are wrong, with the problem and the offset', async () => {
    const f = await boot();
    expect(await f.post('/api/templates', body({ text: 'ab {{Bad}}' }))).toEqual({
      status: 400,
      body: { code: 'invalid-request', reason: 'text:syntax:3' },
    });
    const many = Array.from({ length: 21 }, (_, i) => `{{p${i}}}`).join(' ');
    expect((await f.post('/api/templates', body({ text: many }))).body).toMatchObject({
      reason: expect.stringMatching(/^text:too-many:/),
    });
    const t = await created(f);
    expect((await f.put(`/api/templates/${t.id}`, body({ text: '{{' }))).body).toEqual({
      code: 'invalid-request',
      reason: 'text:syntax:0',
    });
    // An escaped brace is plain text.
    expect((await f.post('/api/templates', body({ name: 'esc', text: 'a \\{{b}}' }))).status).toBe(
      201,
    );
  });

  it('keeps at most 100 templates', async () => {
    const f = await boot();
    for (let i = 0; i < MAX_TEMPLATES; i++) {
      f.store.createTemplate({ name: `t${i}`, text: 'x', provider: 'claude' }, 1);
    }
    expect((await f.post('/api/templates', body({ name: 'one more' }))).status).toBe(413);
    const id = f.store.listTemplates()[0]?.id ?? '';
    await f.del(`/api/templates/${id}`);
    expect((await f.post('/api/templates', body({ name: 'one more' }))).status).toBe(201);
  });
});

describe('templates: the audit log', () => {
  it('records each change as config.changed without the text', async () => {
    const f = await boot();
    const t = await created(f, { text: 'secret {{thing}}' });
    await f.put(
      `/api/templates/${t.id}`,
      body({ text: 'other secret', projectPath: PROJECT, provider: 'claude' }),
    );
    await f.del(`/api/templates/${t.id}`);
    const events = f.store
      .listAudit({}, { order: 'asc', limit: 500 })
      .events.filter((e) => e.type === 'config.changed');
    const summaries = events.map((e) => e.summary as Msg);
    expect(summaries).toHaveLength(3);
    expect(summaries.map((m) => m.action)).toEqual(['created', 'updated', 'deleted']);
    expect(summaries.map((m) => m.area)).toEqual(['templates', 'templates', 'templates']);
    expect(summaries[1]).toMatchObject({ fields: ['projectPath', 'text'] });
    expect(JSON.stringify(events)).not.toContain('secret');
  });
});

describe('templates: start', () => {
  const start = (f: Awaited<ReturnType<typeof boot>>, id: unknown, over: Msg = {}) =>
    f.post(`/api/templates/${id}/start`, { values: { issue: '42' }, promptId: 'p1', ...over });

  it('fills the text and starts through the session path', async () => {
    const f = await boot();
    const owner = await f.agent('dev-a');
    const t = await created(f, { deviceId: 'dev-a', projectPath: PROJECT });
    const pending = start(f, t.id);
    const msg = await owner.nextStart();
    expect(msg).toMatchObject({
      type: 'runtime.start',
      projectPath: PROJECT,
      provider: 'claude',
      text: 'Fix 42 in src/',
      promptId: 'p1',
    });
    owner.send({ type: 'runtime.started', requestId: msg.requestId, sessionId: NEW_ID });
    expect(await pending).toEqual({
      status: 200,
      body: { ok: true, sessionId: NEW_ID, promptId: 'p1' },
    });
  });

  it('answers a repeated promptId once and sends a single runtime.start', async () => {
    const f = await boot();
    const owner = await f.agent('dev-a');
    const t = await created(f, { deviceId: 'dev-a', projectPath: PROJECT });
    const first = start(f, t.id);
    const msg = await owner.nextStart();
    owner.send({ type: 'runtime.started', requestId: msg.requestId, sessionId: NEW_ID });
    await first;
    const again = await start(f, t.id);
    expect(again).toEqual({ status: 200, body: { ok: true, sessionId: NEW_ID, promptId: 'p1' } });
    expect(owner.starts()).toHaveLength(1);
  });

  it('takes the machine and the directory from the request when the template has none', async () => {
    const f = await boot();
    const owner = await f.agent('dev-b');
    const t = await created(f);
    expect(await start(f, t.id)).toEqual({
      status: 400,
      body: { code: 'invalid-request', promptId: 'p1', reason: 'deviceId' },
    });
    expect(await start(f, t.id, { deviceId: 'dev-b' })).toEqual({
      status: 400,
      body: { code: 'invalid-request', promptId: 'p1', reason: 'projectPath' },
    });
    const pending = start(f, t.id, { deviceId: 'dev-b', projectPath: PROJECT });
    const msg = await owner.nextStart();
    owner.send({ type: 'runtime.started', requestId: msg.requestId, sessionId: NEW_ID });
    expect((await pending).status).toBe(200);
  });

  it('names the values that are missing or that no placeholder takes', async () => {
    const f = await boot();
    const owner = await f.agent('dev-a');
    const t = await created(f, { deviceId: 'dev-a', projectPath: PROJECT });
    expect(await start(f, t.id, { values: {} })).toEqual({
      status: 400,
      body: { code: 'invalid-request', promptId: 'p1', reason: 'values:missing:issue' },
    });
    expect(await start(f, t.id, { values: { issue: '' } })).toMatchObject({
      body: { reason: 'values:missing:issue' },
    });
    expect(await start(f, t.id, { values: { issue: '1', nope: 'x' } })).toEqual({
      status: 400,
      body: { code: 'invalid-request', promptId: 'p1', reason: 'values:unknown:nope' },
    });
    expect(owner.starts()).toHaveLength(0);
  });

  it('refuses a text that is empty or too long once filled', async () => {
    const f = await boot();
    await f.agent('dev-a');
    const blank = await created(f, {
      name: 'blank',
      text: '{{a:}}',
      deviceId: 'dev-a',
      projectPath: PROJECT,
    });
    expect(await start(f, blank.id, { values: {} })).toMatchObject({
      status: 400,
      body: { reason: 'text' },
    });
    const big = await created(f, {
      name: 'big',
      text: '{{a}}{{b}}',
      deviceId: 'dev-a',
      projectPath: PROJECT,
    });
    expect(
      (await start(f, big.id, { values: { a: 'x'.repeat(5000), b: 'y'.repeat(5000) } })).status,
    ).toBe(413);
  });

  it('refuses an unknown template and an unknown or offline machine', async () => {
    const f = await boot();
    expect((await start(f, 'nothing')).status).toBe(404);
    const t = await created(f, { deviceId: 'ghost', projectPath: PROJECT });
    expect(await start(f, t.id)).toEqual({
      status: 409,
      body: { code: 'target-offline', promptId: 'p1' },
    });
    expect(await start(f, t.id, { deviceId: 'also-ghost' })).toMatchObject({
      status: 409,
      body: { code: 'target-offline' },
    });
  });
});

describe('templates: export and import', () => {
  it('exports the machine by name and round-trips through an import', async () => {
    const f = await boot();
    f.store.upsertDevice('dev-a', 'Laptop', 'test');
    await created(f, { deviceId: 'dev-a', projectPath: PROJECT, configuration: 'claude' });
    await created(f, { name: 'Bare', text: 'hello' });
    const file = (await f.get('/api/templates/export')).body;
    expect(file).toEqual({
      v: 1,
      templates: [
        { name: 'Bare', text: 'hello', provider: 'claude' },
        {
          name: 'Fix an issue',
          text: 'Fix {{issue}} in {{path:src/}}',
          device: 'Laptop',
          project: PROJECT,
          provider: 'claude',
          configuration: 'claude',
        },
      ],
    });

    // The same host: every name is taken, so each gets a suffix, and the machine matches by name.
    const again = await f.post('/api/templates/import', { file });
    expect(again.status).toBe(200);
    expect(again.body.dryRun).toBe(false);
    expect(again.body.items).toEqual([
      { name: 'Bare (2)', renamed: true },
      { name: 'Fix an issue (2)', renamed: true, device: 'Laptop', deviceId: 'dev-a' },
    ]);
    const stored = (again.body.templates as Msg[]).find((t) => t.name === 'Fix an issue (2)');
    expect(stored).toMatchObject({
      deviceId: 'dev-a',
      projectPath: PROJECT,
      configuration: 'claude',
      text: 'Fix {{issue}} in {{path:src/}}',
    });
    expect((await f.get('/api/templates')).body.templates).toHaveLength(4);
  });

  it('imports into an empty host as it was exported', async () => {
    const a = await boot();
    a.store.upsertDevice('dev-a', 'Laptop', 'test');
    await created(a, { deviceId: 'dev-a', projectPath: PROJECT, config: { model: 'opus' } });
    const file = (await a.get('/api/templates/export')).body;

    const b = await boot();
    b.store.upsertDevice('dev-z', 'Laptop', 'test');
    const res = await b.post('/api/templates/import', { file });
    expect(res.body.items).toEqual([
      { name: 'Fix an issue', renamed: false, device: 'Laptop', deviceId: 'dev-z' },
    ]);
    expect((await b.get('/api/templates/export')).body).toEqual(file);
  });

  it('shows what would happen on a dry run and stores nothing', async () => {
    const f = await boot();
    await created(f);
    const file = (await f.get('/api/templates/export')).body;
    const res = await f.post('/api/templates/import', { file, dryRun: true });
    expect(res.body).toMatchObject({
      dryRun: true,
      items: [{ name: 'Fix an issue (2)', renamed: true }],
      templates: [],
    });
    expect((await f.get('/api/templates')).body.templates).toHaveLength(1);
  });

  it('leaves the machine open when none matches, or takes the one the user chose', async () => {
    const f = await boot();
    f.store.upsertDevice('dev-a', 'Laptop', 'test');
    const file = {
      v: 1,
      templates: [{ name: 'T', text: 'x', device: 'Desktop', provider: 'claude' }],
    };
    const open = await f.post('/api/templates/import', { file, dryRun: true });
    expect(open.body.items).toEqual([{ name: 'T', renamed: false, device: 'Desktop' }]);
    const chosen = await f.post('/api/templates/import', {
      file,
      machines: { Desktop: 'dev-a' },
    });
    expect((chosen.body.templates as Msg[])[0]).toMatchObject({ deviceId: 'dev-a' });
    expect(
      (await f.post('/api/templates/import', { file, machines: { Desktop: 'nowhere' } })).body,
    ).toEqual({ code: 'invalid-request', reason: 'machines' });
  });

  it('refuses a file of another version or shape, a bad placeholder and too many templates', async () => {
    const f = await boot();
    const entry = { name: 'T', text: 'x', provider: 'claude' };
    for (const file of [
      { v: 2, templates: [entry] },
      { v: 1, templates: [{ ...entry, extra: true }] },
      { v: 1 },
    ]) {
      expect((await f.post('/api/templates/import', { file })).status).toBe(400);
    }
    expect(
      (
        await f.post('/api/templates/import', {
          file: { v: 1, templates: [entry, { ...entry, name: 'U', text: '{{' }] },
        })
      ).body,
    ).toEqual({ code: 'invalid-request', reason: 'file.1.text:syntax:0' });
    expect((await f.get('/api/templates')).body.templates).toEqual([]);

    for (let i = 0; i < MAX_TEMPLATES - 1; i++) {
      f.store.createTemplate({ name: `t${i}`, text: 'x', provider: 'claude' }, 1);
    }
    const two = { v: 1, templates: [entry, { ...entry, name: 'U' }] };
    expect((await f.post('/api/templates/import', { file: two })).status).toBe(413);
    expect((await f.get('/api/templates')).body.templates).toHaveLength(MAX_TEMPLATES - 1);
  });
});

describe('freeName', () => {
  it('adds the first free suffix and stays within 80 characters', () => {
    expect(freeName('a', new Set())).toBe('a');
    expect(freeName('a', new Set(['a']))).toBe('a (2)');
    expect(freeName('a', new Set(['a', 'a (2)']))).toBe('a (3)');
    const long = 'n'.repeat(80);
    const named = freeName(long, new Set([long]));
    expect(named).toBe(`${'n'.repeat(76)} (2)`);
    expect(named).toHaveLength(80);
  });
});
