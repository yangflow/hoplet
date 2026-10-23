import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { BUILTIN_PERMISSION_RULES, PROTOCOL_VERSION } from '@hoplet/shared';
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

async function boot(options: { rulesRetryMs?: number } = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-rule-sync-'));
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
  const control = createControl(store, options);
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
  const agent = async (deviceId: 'dev-a' | 'dev-b') => {
    const token = tokens[deviceId];
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
      headers: { authorization: `Bearer ${token}` },
    });
    sockets.push(ws);
    const got: Msg[] = [];
    ws.on('message', (data) => got.push(JSON.parse(String(data)) as Msg));
    await new Promise<void>((r) => ws.once('open', () => r()));
    const send = (msg: Msg) => ws.send(JSON.stringify({ v: PROTOCOL_VERSION, ...msg }));
    send({ type: 'device.hello', deviceId, name: deviceId, version: 'test', providers: [] });
    const rules = () => got.filter((m) => m.type === 'device.rules');
    const waitForRules = async (count: number): Promise<Msg> => {
      for (let i = 0; i < 100; i++) {
        if (rules().length >= count) return rules()[count - 1] as Msg;
        await new Promise((r) => setTimeout(r, 20));
      }
      throw new Error(`timed out waiting for device.rules #${count}`);
    };
    const settle = () => new Promise((r) => setTimeout(r, 120));
    return { send, rules, waitForRules, settle };
  };
  return { store, control, call, agent, post: (p: string, b?: unknown) => call('POST', p, b) };
}

const rule = (over: Msg = {}) => ({
  scope: 'global',
  tool: 'run',
  match: { command: 'rm', anyArg: ['-rf'] },
  action: 'ask',
  ...over,
});

describe('permission rules: the push to the agent', () => {
  it('sends the set after hello: the built-ins, the rules of this device and of its sessions', async () => {
    const f = await boot();
    await f.post('/api/permission-rules', rule({ note: 'global one' }));
    await f.post(
      '/api/permission-rules',
      rule({ scope: 'project', deviceId: 'dev-a', projectPath: '/work/app', action: 'allow' }),
    );
    await f.post(
      '/api/permission-rules',
      rule({ scope: 'project', deviceId: 'dev-b', projectPath: '/work/other' }),
    );
    await f.post('/api/permission-rules', rule({ scope: 'session', sessionId: SESSION }));

    const a = await f.agent('dev-a');
    const set = await a.waitForRules(1);
    expect(set.v).toBe(PROTOCOL_VERSION);
    const global = set.global as Msg[];
    expect(global.slice(0, BUILTIN_PERMISSION_RULES.length)).toEqual(BUILTIN_PERMISSION_RULES);
    expect(global).toHaveLength(BUILTIN_PERMISSION_RULES.length + 1);
    const projects = set.projects as { projectPath: string; rule: Msg }[];
    expect(projects.map((p) => p.projectPath)).toEqual(['/work/app']);
    const sessions = set.sessions as { sessionId: string; rule: Msg }[];
    expect(sessions.map((s) => s.sessionId)).toEqual([SESSION]);
    // The body is exactly what the agent reads: no target, no timestamps.
    for (const r of [...global, projects[0]?.rule, sessions[0]?.rule]) {
      expect(Object.keys(r as Msg).sort()).toEqual(
        ['action', 'builtin', 'enabled', 'id', 'match', 'note', 'scope', 'tool'].sort(),
      );
    }

    const b = await f.agent('dev-b');
    const other = await b.waitForRules(1);
    expect((other.projects as { projectPath: string }[]).map((p) => p.projectPath)).toEqual([
      '/work/other',
    ]);
    expect(other.sessions).toEqual([]);
  });

  it('sends again when a rule changes, and only to the devices it reaches', async () => {
    const f = await boot();
    const a = await f.agent('dev-a');
    const b = await f.agent('dev-b');
    const first = await a.waitForRules(1);
    await b.waitForRules(1);

    const created = await f.post(
      '/api/permission-rules',
      rule({ scope: 'project', deviceId: 'dev-a', projectPath: '/work/app' }),
    );
    expect(created.status).toBe(201);
    const second = await a.waitForRules(2);
    expect(second.revision as number).toBeGreaterThan(first.revision as number);
    await b.settle();
    expect(b.rules()).toHaveLength(1);

    const global = await f.post('/api/permission-rules', rule());
    const third = await a.waitForRules(3);
    await b.waitForRules(2);
    expect(third.revision as number).toBeGreaterThan(second.revision as number);

    expect(
      (await f.call('PUT', `/api/permission-rules/default.force-push`, { enabled: true })).status,
    ).toBe(200);
    await a.settle();
    expect(a.rules()).toHaveLength(3); // already on: no change, no push

    expect(
      (
        await f.call('PUT', `/api/permission-rules/default.force-push`, {
          enabled: false,
          confirm: 'default.force-push',
        })
      ).status,
    ).toBe(200);
    const fourth = await a.waitForRules(4);
    const off = (fourth.global as Msg[]).find((r) => r.id === 'default.force-push');
    expect(off?.enabled).toBe(false);

    expect((await f.call('DELETE', `/api/permission-rules/${global.body.id}`)).status).toBe(200);
    const fifth = await a.waitForRules(5);
    expect((fifth.global as Msg[]).some((r) => r.id === global.body.id)).toBe(false);
  });

  it('sends the session rules to the new owner after the session changes hands', async () => {
    const f = await boot();
    const a = await f.agent('dev-a');
    const b = await f.agent('dev-b');
    await f.post('/api/permission-rules', rule({ scope: 'session', sessionId: SESSION }));
    expect(((await a.waitForRules(2)).sessions as unknown[]).length).toBe(1);
    await b.waitForRules(1);
    expect(((b.rules()[0] as Msg).sessions as unknown[]).length).toBe(0);

    const size = f.store.getSession(SESSION)?.byteSize ?? 0;
    expect(
      f.store.handoffOwner(SESSION, 'dev-a', 'dev-b', size, 0, '/w', 'hoplet/w', null, ''),
    ).toBe(1);
    f.control.syncRules();
    expect(((await b.waitForRules(2)).sessions as unknown[]).length).toBe(1);
    expect(((await a.waitForRules(3)).sessions as unknown[]).length).toBe(0);
  });

  it('sends the inherited rules of a child as soon as the child session first syncs', async () => {
    const f = await boot();
    const a = await f.agent('dev-a');
    const first = await a.waitForRules(1);
    const created = await f.post(
      '/api/permission-rules',
      rule({ scope: 'session', sessionId: SESSION, action: 'deny' }),
    );
    await f.post(
      '/api/permission-rules',
      rule({ scope: 'session', sessionId: SESSION, action: 'allow' }),
    );
    await a.waitForRules(3);
    // The child is known to the core before its session is: it has no owner, so no rules to send yet.
    const child = 'c0c0c0c0-3c1a-4d6e-9a53-0c6f0f3f2a12';
    f.store.recordChild(child, [SESSION], Date.now(), 30 * 60_000);
    a.send({
      type: 'session.append',
      sessionId: child,
      provider: 'claude',
      projectDir: 'p',
      filePath: '/y',
      byteStart: 0,
      encoding: 'plain',
      lines: '{"type":"user"}\n',
    });
    const set = await a.waitForRules(4);
    expect(set.revision as number).toBeGreaterThan(first.revision as number);
    expect(set.inherited).toEqual([
      { sessionId: child, rule: expect.objectContaining({ id: created.body.id, action: 'deny' }) },
    ]);
  });

  it('records the revision the agent applied and does not count a refused one', async () => {
    const f = await boot();
    const a = await f.agent('dev-a');
    const set = await a.waitForRules(1);
    expect(f.control.appliedRulesRevision('dev-a')).toBeNull();
    a.send({ type: 'device.rules.applied', revision: set.revision });
    await a.settle();
    expect(f.control.appliedRulesRevision('dev-a')).toBe(set.revision);
    // A refused push is logged and not recorded: the agent keeps its previous set.
    a.send({ type: 'device.rules.applied', revision: 99, error: 'unknown field' });
    await a.settle();
    expect(f.control.appliedRulesRevision('dev-a')).toBe(set.revision);

    await f.post('/api/permission-rules', rule());
    const next = await a.waitForRules(2);
    expect(next.revision as number).toBeGreaterThan(set.revision as number);
  });
});

describe('permission rules: which machines applied the latest revision', () => {
  type Machine = {
    deviceId: string;
    online: boolean;
    revision: number;
    applied: number | null;
    appliedAt: number | null;
    pushedAt: number | null;
    retrying: boolean;
  };
  const machines = async (f: Awaited<ReturnType<typeof boot>>) => {
    const res = await f.call('GET', '/api/permission-rules/machines');
    expect(res.status).toBe(200);
    return res.body as unknown as { latest: number; machines: Machine[] };
  };

  it('lists the machines with what they confirmed, and keeps the confirmation after they go offline', async () => {
    const f = await boot({ rulesRetryMs: 60_000 });
    const a = await f.agent('dev-a');
    const set = await a.waitForRules(1);
    let list = await machines(f);
    const mine = () => list.machines.find((m) => m.deviceId === 'dev-a') as Machine;
    expect(mine()).toMatchObject({ online: true, applied: null, appliedAt: null, retrying: true });
    expect(mine().pushedAt).toEqual(expect.any(Number));

    a.send({ type: 'device.rules.applied', revision: set.revision });
    await a.settle();
    list = await machines(f);
    expect(mine()).toMatchObject({ applied: set.revision, retrying: false });
    expect(mine().appliedAt).toEqual(expect.any(Number));
    expect(list.latest).toBe(set.revision);

    sockets[0]?.terminate();
    await a.settle();
    list = await machines(f);
    expect(mine()).toMatchObject({ online: false, applied: set.revision });
  });

  it('sends the rules again when no confirmation comes, a limited number of times', async () => {
    const f = await boot({ rulesRetryMs: 40 });
    const a = await f.agent('dev-a');
    await a.waitForRules(3);
    await new Promise((r) => setTimeout(r, 400));
    expect(a.rules()).toHaveLength(6); // the push and five resends
  });

  it('stops resending once the agent confirms, and after it refuses', async () => {
    const f = await boot({ rulesRetryMs: 60 });
    const a = await f.agent('dev-a');
    const set = await a.waitForRules(1);
    a.send({ type: 'device.rules.applied', revision: set.revision });
    await new Promise((r) => setTimeout(r, 400));
    expect(a.rules()).toHaveLength(1);

    const b = await f.agent('dev-b');
    const sent = await b.waitForRules(1);
    b.send({ type: 'device.rules.applied', revision: sent.revision, error: 'unknown field' });
    await new Promise((r) => setTimeout(r, 400));
    expect(b.rules()).toHaveLength(1);
    expect((await machines(f)).machines.find((m) => m.deviceId === 'dev-b')?.retrying).toBe(false);
  });

  it('pushes again on request, and refuses an unknown or an offline machine', async () => {
    const f = await boot({ rulesRetryMs: 60_000 });
    const a = await f.agent('dev-a');
    await a.waitForRules(1);
    const res = await f.post('/api/permission-rules/machines/dev-a/push');
    expect(res.status).toBe(200);
    await a.waitForRules(2);
    expect((await f.post('/api/permission-rules/machines/nobody/push')).status).toBe(404);
    expect((await f.post('/api/permission-rules/machines/dev-b/push')).status).toBe(409);
  });
});
