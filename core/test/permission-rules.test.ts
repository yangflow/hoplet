import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { BUILTIN_PERMISSION_RULES, MAX_PERMISSION_RULES } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { createApi } from '../src/api/index.ts';
import { createControl } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

const HOST = 'host-token-0123456789abcdef';
const SESSION = '3b1f1b0e-3c1a-4d6e-9a53-0c6f0f3f2a11';
const ID = 'default.force-push';

const dirs: string[] = [];
const servers: Server[] = [];

afterEach(async () => {
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

type Msg = Record<string, unknown>;

async function boot() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-rules-'));
  dirs.push(dir);
  const store = new Store(dir);
  store.mintDevice('dev-a', 'dev-a');
  store.mintDevice('dev-b', 'dev-b');
  store.append(
    { sessionId: SESSION, provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'dev-a' },
    ['{"type":"user"}'],
  );
  const control = createControl(store, { startTimeoutMs: 400 });
  const server = createServer();
  attachIngest(server, store, control);
  createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control).attach(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  servers.push(server);
  const port = (server.address() as AddressInfo).port;
  const call = async (method: string, p: string, body?: unknown, token: string | null = HOST) => {
    const res = await fetch(`http://127.0.0.1:${port}${p}`, {
      method,
      headers: {
        ...(token ? { authorization: `Bearer ${token}` } : {}),
        'content-type': 'application/json',
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    const text = await res.text();
    return { status: res.status, body: (text ? JSON.parse(text) : {}) as Msg };
  };
  return {
    store,
    call,
    get: (p: string) => call('GET', p),
    post: (p: string, b?: unknown) => call('POST', p, b),
    put: (p: string, b?: unknown) => call('PUT', p, b),
    del: (p: string) => call('DELETE', p),
  };
}

type Fixture = Awaited<ReturnType<typeof boot>>;

const rule = (over: Msg = {}) => ({
  scope: 'global',
  tool: 'run',
  match: { command: 'rm', anyArg: ['-rf'] },
  action: 'ask',
  ...over,
});

const created = async (f: Fixture, over: Msg = {}) => {
  const res = await f.post('/api/permission-rules', rule(over));
  expect(res.status, JSON.stringify(res.body)).toBe(201);
  return res.body;
};

const rev = async (f: Fixture, deviceId: string) =>
  (await f.get(`/api/permission-rules?deviceId=${deviceId}`)).body.revision as number;

const revs = async (f: Fixture): Promise<[number, number]> => [
  await rev(f, 'dev-a'),
  await rev(f, 'dev-b'),
];

describe('permission rules: the routes', () => {
  it('lists the eight built-ins first, identical to the agent table', async () => {
    const f = await boot();
    const { body } = await f.get('/api/permission-rules');
    const rules = body.rules as Msg[];
    expect(rules).toHaveLength(8);
    expect(rules).toEqual(BUILTIN_PERMISSION_RULES);
    expect(body.revision).toBeUndefined();
  });

  it('creates, reads, updates and deletes a rule of the user', async () => {
    const f = await boot();
    const r = await created(f, { note: 'careful' });
    expect(r).toMatchObject({
      scope: 'global',
      tool: 'run',
      action: 'ask',
      note: 'careful',
      enabled: true,
      builtin: false,
    });
    expect(typeof r.id).toBe('string');
    const listed = (await f.get('/api/permission-rules')).body.rules as Msg[];
    expect(listed).toHaveLength(9);
    expect(listed[8]).toEqual(r);

    const put = await f.put(
      `/api/permission-rules/${r.id}`,
      rule({ action: 'deny', enabled: false }),
    );
    expect(put.status).toBe(200);
    expect(put.body).toMatchObject({ id: r.id, action: 'deny', enabled: false, note: '' });

    const del = await f.del(`/api/permission-rules/${r.id}`);
    expect(del.status).toBe(200);
    expect((del.body.rules as Msg[]).length).toBe(8);
    expect((await f.del(`/api/permission-rules/${r.id}`)).status).toBe(404);
    expect((await f.put(`/api/permission-rules/${r.id}`, rule())).status).toBe(404);
  });

  it('keeps the rules in the order they were made', async () => {
    const f = await boot();
    const a = await created(f, { note: 'a' });
    const b = await created(f, { note: 'b' });
    await f.put(`/api/permission-rules/${a.id}`, rule({ note: 'a2' }));
    const listed = (await f.get('/api/permission-rules')).body.rules as Msg[];
    expect(listed.slice(8).map((r) => r.id)).toEqual([a.id, b.id]);
  });

  it('refuses unknown fields, a bad action, scope or note', async () => {
    const f = await boot();
    for (const bad of [
      rule({ extra: 1 }),
      rule({ match: { command: 'rm', extra: true } }),
      rule({ action: 'maybe' }),
      rule({ scope: 'machine' }),
      rule({ note: 'n'.repeat(201) }),
      rule({ id: 'mine' }),
      rule({ builtin: true }),
      rule({ tool: '' }),
    ]) {
      const res = await f.post('/api/permission-rules', bad);
      expect(res.status, JSON.stringify(bad)).toBe(400);
    }
  });

  it('refuses a rule the agent would refuse and accepts one it would take', async () => {
    const f = await boot();
    for (const bad of [
      rule({ tool: 'launch' }),
      rule({ tool: '*,run' }),
      rule({ tool: 'run,read' }),
      rule({ tool: 'mcp:server' }),
      rule({ match: { paths: ['/x'] } }),
      rule({ match: { anyArg: ['-f'] } }),
      rule({ tool: 'read', match: { except: ['/x'] } }),
      rule({ tool: 'read', match: { paths: ['relative/path'] } }),
      rule({ tool: 'fetch', match: { domains: ['a.*.com'] } }),
      rule({ tool: 'fetch', match: { command: 'x' } }),
    ]) {
      const res = await f.post('/api/permission-rules', bad);
      expect(res.status, JSON.stringify(bad)).toBe(400);
      expect(res.body).toMatchObject({ code: 'invalid-request' });
      expect(String(res.body.reason)).toMatch(/^rule: /);
    }
    for (const good of [
      rule({ tool: '*', match: {} }),
      rule({ tool: 'read,write', match: { paths: ['~/x/**', '{workspace}/**'], except: ['/y'] } }),
      rule({ tool: 'fetch', match: { domains: ['*.example.com', 'api.test:8080'] } }),
      rule({ tool: 'mcp:git/push', match: {} }),
      rule({ match: { or: [{ command: 'git', anyArg: ['-f'] }, { command: 'rm' }] } }),
    ]) {
      const res = await f.post('/api/permission-rules', good);
      expect(res.status, JSON.stringify(res.body)).toBe(201);
    }
  });

  it('checks the scope against its targets', async () => {
    const f = await boot();
    const refused = async (over: Msg, reason: string) =>
      expect(await f.post('/api/permission-rules', rule(over))).toEqual({
        status: 400,
        body: { code: 'invalid-request', reason },
      });
    await refused({ deviceId: 'dev-a' }, 'scope');
    await refused({ sessionId: SESSION }, 'scope');
    await refused({ scope: 'project' }, 'scope');
    await refused({ scope: 'project', deviceId: 'dev-a' }, 'scope');
    await refused({ scope: 'project', deviceId: 'nope', projectPath: '/work/app' }, 'deviceId');
    await refused({ scope: 'session' }, 'scope');
    await refused({ scope: 'session', sessionId: SESSION, deviceId: 'dev-a' }, 'scope');
    await refused(
      { scope: 'session', sessionId: '9b1f1b0e-3c1a-4d6e-9a53-0c6f0f3f2a99' },
      'sessionId',
    );

    const p = await created(f, { scope: 'project', deviceId: 'dev-a', projectPath: '/work/app' });
    expect(p).toMatchObject({ deviceId: 'dev-a', projectPath: '/work/app' });
    const s = await created(f, { scope: 'session', sessionId: SESSION });
    expect(s).toMatchObject({ sessionId: SESSION });
  });

  it('filters the list by scope and target', async () => {
    const f = await boot();
    await created(f);
    const p1 = await created(f, { scope: 'project', deviceId: 'dev-a', projectPath: '/work/app' });
    await created(f, { scope: 'project', deviceId: 'dev-b', projectPath: '/work/app' });
    const s = await created(f, { scope: 'session', sessionId: SESSION });
    const ids = async (q: string) =>
      ((await f.get(`/api/permission-rules${q}`)).body.rules as Msg[])
        .filter((r) => !r.builtin)
        .map((r) => r.id);
    expect(await ids('?scope=session')).toEqual([s.id]);
    expect(await ids('?scope=project&deviceId=dev-a')).toEqual([p1.id]);
    expect(
      await ids(`?scope=project&deviceId=dev-a&projectPath=${encodeURIComponent('/work/other')}`),
    ).toEqual([]);
    expect(await ids(`?scope=session&sessionId=${SESSION}`)).toEqual([s.id]);
    expect((await f.get('/api/permission-rules?scope=nope')).status).toBe(400);
    expect((await f.get('/api/permission-rules?bogus=1')).status).toBe(400);
  });

  it('stops at the limit', async () => {
    const f = await boot();
    for (let i = 0; i < MAX_PERMISSION_RULES; i++) {
      f.store.createPermissionRule(
        { scope: 'global', tool: 'run', match: {}, action: 'ask', note: '', enabled: true },
        i,
      );
    }
    expect((await f.post('/api/permission-rules', rule())).body).toMatchObject({
      code: 'too-large',
    });
  });

  it('wants the host token, not a machine token', async () => {
    const f = await boot();
    const r = await created(f);
    const { token } = f.store.mintDevice('dev-c', 'dev-c');
    for (const t of [null, 'wrong-token', token]) {
      expect((await f.call('GET', '/api/permission-rules', undefined, t)).status).toBe(401);
      expect((await f.call('POST', '/api/permission-rules', rule(), t)).status).toBe(401);
      expect((await f.call('PUT', `/api/permission-rules/${r.id}`, rule(), t)).status).toBe(401);
      expect((await f.call('DELETE', `/api/permission-rules/${r.id}`, undefined, t)).status).toBe(
        401,
      );
    }
    expect(f.store.listPermissionRules()).toHaveLength(9);
  });
});

describe('permission rules: the built-ins', () => {
  it('are not edited or deleted', async () => {
    const f = await boot();
    expect(await f.del(`/api/permission-rules/${ID}`)).toEqual({
      status: 400,
      body: { code: 'invalid-request', reason: 'builtin' },
    });
    expect((await f.del('/api/permission-rules/default.nope')).status).toBe(404);
    expect(await f.put(`/api/permission-rules/${ID}`, rule())).toEqual({
      status: 400,
      body: { code: 'invalid-request', reason: 'builtin' },
    });
    expect(
      (await f.put(`/api/permission-rules/${ID}`, { enabled: true, action: 'allow' })).status,
    ).toBe(400);
    expect((await f.put('/api/permission-rules/default.nope', { enabled: false })).status).toBe(
      404,
    );
    expect((await f.get('/api/permission-rules')).body.rules).toEqual(BUILTIN_PERMISSION_RULES);
  });

  it('turn off only with their ID typed back, and on without it', async () => {
    const f = await boot();
    const url = `/api/permission-rules/${ID}`;
    for (const confirm of [undefined, '', 'default.other', 'force-push']) {
      const res = await f.put(url, {
        enabled: false,
        ...(confirm === undefined ? {} : { confirm }),
      });
      expect(res).toEqual({ status: 400, body: { code: 'invalid-request', reason: 'confirm' } });
    }
    const off = await f.put(url, { enabled: false, confirm: ID });
    expect(off.status).toBe(200);
    expect(off.body).toMatchObject({ id: ID, enabled: false, builtin: true, action: 'ask' });
    const listed = (await f.get('/api/permission-rules')).body.rules as Msg[];
    expect(listed.filter((r) => !r.enabled).map((r) => r.id)).toEqual([ID]);

    const on = await f.put(url, { enabled: true });
    expect(on.body).toMatchObject({ id: ID, enabled: true });
    expect((await f.put(url, { enabled: true })).status).toBe(200);
  });

  it('raise the revision of every machine only when a switch changes', async () => {
    const f = await boot();
    const url = `/api/permission-rules/${ID}`;
    const [a, b] = await revs(f);
    await f.put(url, { enabled: true });
    expect(await revs(f)).toEqual([a, b]);
    await f.put(url, { enabled: false, confirm: ID });
    expect(await revs(f)).toEqual([a + 1, b + 1]);
    await f.put(url, { enabled: false, confirm: ID });
    expect(await revs(f)).toEqual([a + 1, b + 1]);
  });
});

describe('permission rules: the revision', () => {
  it('grows for every machine with a global change', async () => {
    const f = await boot();
    const [a, b] = await revs(f);
    const r = await created(f);
    expect(await revs(f)).toEqual([a + 1, b + 1]);
    await f.put(`/api/permission-rules/${r.id}`, rule({ action: 'deny' }));
    expect(await revs(f)).toEqual([a + 2, b + 2]);
    await f.del(`/api/permission-rules/${r.id}`);
    expect(await revs(f)).toEqual([a + 3, b + 3]);
  });

  it('grows for the machine of a project rule only', async () => {
    const f = await boot();
    const [a, b] = await revs(f);
    const r = await created(f, { scope: 'project', deviceId: 'dev-a', projectPath: '/work/app' });
    expect(await revs(f)).toEqual([a + 1, b]);
    // Moving the rule to another machine changes both.
    await f.put(
      `/api/permission-rules/${r.id}`,
      rule({ scope: 'project', deviceId: 'dev-b', projectPath: '/work/app' }),
    );
    expect(await revs(f)).toEqual([a + 2, b + 1]);
    await f.del(`/api/permission-rules/${r.id}`);
    expect(await revs(f)).toEqual([a + 2, b + 2]);
  });

  it('grows for the owner of a session rule, which a session query also reports', async () => {
    const f = await boot();
    const [a, b] = await revs(f);
    await created(f, { scope: 'session', sessionId: SESSION });
    expect(await revs(f)).toEqual([a + 1, b]);
    const bySession = await f.get(`/api/permission-rules?sessionId=${SESSION}`);
    expect(bySession.body.revision).toBe(a + 1);
  });

  it('does not move when a request is refused', async () => {
    const f = await boot();
    const before = await revs(f);
    await f.post('/api/permission-rules', rule({ tool: 'launch' }));
    await f.post('/api/permission-rules', rule({ extra: 1 }));
    expect(await revs(f)).toEqual(before);
  });

  it('drops the rules of a session that is purged', async () => {
    const f = await boot();
    await created(f, { scope: 'session', sessionId: SESSION });
    const a = await rev(f, 'dev-a');
    f.store.purgeSession(SESSION);
    expect((await f.get('/api/permission-rules?scope=session')).body.rules).toEqual([]);
    expect(await rev(f, 'dev-a')).toBe(a + 1);
  });
});

describe('permission rules: the audit log', () => {
  const changes = (f: Fixture) =>
    f.store
      .listAudit({ type: 'config.changed' }, { order: 'asc', limit: 100 })
      .events.map((e) => e.summary as Msg);

  it('records create, update, delete and a built-in switch as config.changed without the patterns', async () => {
    const f = await boot();
    const made = await created(f, {
      match: { command: 'deploy-secret-tool' },
      note: 'private note',
    });
    const url = `/api/permission-rules/${made.id}`;
    expect(
      (
        await f.put(
          url,
          rule({ match: { command: 'other-secret-tool' }, note: 'private note', action: 'deny' }),
        )
      ).status,
    ).toBe(200);
    expect((await f.del(url)).status).toBe(200);
    expect(
      (await f.put(`/api/permission-rules/${ID}`, { enabled: false, confirm: ID })).status,
    ).toBe(200);
    expect(
      (await f.put(`/api/permission-rules/${ID}`, { enabled: false, confirm: ID })).status,
    ).toBe(200);

    const summaries = changes(f);
    expect(summaries.map((m) => [m.area, m.action, m.id])).toEqual([
      ['permission-rules', 'created', made.id],
      ['permission-rules', 'updated', made.id],
      ['permission-rules', 'deleted', made.id],
      ['permission-rules', 'updated', ID],
    ]);
    expect(summaries[0]).toMatchObject({
      current: { scope: 'global', tool: 'run', action: 'ask', enabled: true },
    });
    expect(summaries[1]).toMatchObject({
      fields: ['action', 'match'],
      previous: { action: 'ask' },
      current: { action: 'deny' },
    });
    expect(summaries[3]).toMatchObject({
      fields: ['enabled'],
      previous: { enabled: true },
      current: { enabled: false },
    });
    const events = f.store.listAudit({ type: 'config.changed' }, { limit: 100 }).events;
    expect(events.every((e) => e.actorKind === 'operator' && e.channel === 'rest')).toBe(true);
    expect(JSON.stringify(events)).not.toMatch(/secret-tool|private note/);
    expect(await f.store.verifyAudit()).toMatchObject({ ok: true, firstBreak: null });
  });

  it('records nothing for a refused request or a missing rule', async () => {
    const f = await boot();
    await f.post('/api/permission-rules', rule({ tool: '' }));
    await f.del('/api/permission-rules/nope');
    await f.put('/api/permission-rules/nope', rule());
    expect(changes(f)).toEqual([]);
  });
});
