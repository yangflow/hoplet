import { createHash } from 'node:crypto';
import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  DEFAULT_SESSION_MCP_SETTINGS,
  PROTOCOL_VERSION,
  type SessionMcpSettings,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { WebSocket } from 'ws';
import { createApi } from '../src/api/index.ts';
import {
  type ConfigFileApply,
  type ConfigFilePreview,
  type ConfigFiles,
  createControl,
} from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

// biome-ignore lint/suspicious/noExplicitAny: parsed wire JSON, read by field name
type Json = Record<string, any>;

const HOST = 'host-token-0123456789abcdef';
const DAY = 24 * 3_600_000;

const dirs: string[] = [];
const servers: Server[] = [];
const sockets: WebSocket[] = [];

afterEach(async () => {
  for (const ws of sockets.splice(0)) ws.terminate();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

const sha = (s: string) => createHash('sha256').update(s, 'utf8').digest('hex');

const CONFIGURATION = {
  id: 'claude',
  name: 'Claude Code',
  extends: 'claude',
  enabled: true,
  env: [],
  defaults: {},
};
const CAPABILITIES = {
  permissions: true,
  interrupt: true,
  config: [
    {
      type: 'select',
      id: 'model',
      name: 'Model',
      category: 'model',
      currentValue: 'sonnet',
      options: [
        { value: 'sonnet', name: 'Sonnet' },
        { value: 'opus', name: 'Opus' },
        { value: 'unlimited', name: 'Unlimited', risk: 'high' },
      ],
    },
    {
      type: 'select',
      id: 'mode',
      name: 'Mode',
      category: 'mode',
      currentValue: 'default',
      options: [
        { value: 'default', name: 'Default' },
        { value: 'bypass', name: 'Bypass' },
      ],
    },
  ],
};

/** A stand-in for the owner's agent: files by key, and what it was asked. */
function fakeFiles() {
  const disk = new Map<string, string>();
  const calls = { preview: 0, apply: [] as Json[] };
  const behaviour: {
    preview: ConfigFilePreview | null;
    apply: ConfigFileApply | null;
  } = { preview: null, apply: null };
  const files: ConfigFiles = {
    async preview(_device, _session, objects) {
      calls.preview++;
      if (behaviour.preview) return behaviour.preview;
      return {
        ok: true,
        objects: objects.map((o) => {
          const now = disk.get(`${o.kind}:${o.name}`) ?? null;
          return {
            kind: o.kind,
            name: o.name,
            currentHash: now === null ? '' : sha(now),
            previous: now,
            diff: `@@ ${o.kind}:${o.name} @@\n`,
          };
        }),
      };
    },
    async apply(_device, input) {
      calls.apply.push(input);
      if (behaviour.apply) return behaviour.apply;
      for (const o of input.objects) {
        const key = `${o.kind}:${o.name}`;
        const now = disk.get(key) ?? null;
        if ((now === null ? '' : sha(now)) !== o.baseHash) return { ok: false, error: 'stale' };
      }
      for (const o of input.objects) {
        const key = `${o.kind}:${o.name}`;
        if (o.content === null) disk.delete(key);
        else disk.set(key, o.content);
      }
      return { ok: true };
    },
  };
  return { files, disk, calls, behaviour };
}

/** Session a1 on dev-a (connected), a2 on dev-d (stored, offline). */
async function boot() {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-config-changes-'));
  dirs.push(dir);
  const store = new Store(dir);
  for (const [id, deviceId] of [
    ['a1', 'dev-a'],
    ['a2', 'dev-a'],
    ['d1', 'dev-d'],
  ] as const) {
    store.append(
      {
        sessionId: id,
        provider: 'claude',
        projectDir: 'p',
        filePath: `/x/${id}`,
        deviceId,
      },
      [
        JSON.stringify({
          type: 'user',
          cwd: '/work/a',
          message: { role: 'user', content: 'hello' },
        }),
      ],
    );
  }
  const fake = fakeFiles();
  const clock = { now: Date.now() };
  const control = createControl(store, {
    promptTimeoutMs: 600,
    workspaceTimeoutMs: 150,
    now: () => clock.now,
    configFiles: fake.files,
  });
  const server = createServer();
  attachIngest(server, store, control);
  createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control).attach(server);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  servers.push(server);
  const port = (server.address() as AddressInfo).port;

  const rest = async (method: string, p: string, token: string, body?: unknown) => {
    const res = await fetch(`http://127.0.0.1:${port}${p}`, {
      method,
      headers: {
        authorization: `Bearer ${token}`,
        ...(body === undefined ? {} : { 'content-type': 'application/json' }),
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    const text = await res.text();
    return { status: res.status, body: (text ? JSON.parse(text) : null) as Json };
  };

  const received: Json[] = [];
  const connect = async (id: string) => {
    const device = store.mintDevice(id, id);
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
      headers: { authorization: `Bearer ${device.token}` },
    });
    sockets.push(ws);
    const send = (msg: Json) => ws.send(JSON.stringify({ v: PROTOCOL_VERSION, ...msg }));
    ws.on('message', (data) => received.push(JSON.parse(String(data))));
    await new Promise<void>((resolve, reject) => {
      ws.once('error', reject);
      ws.once('message', () => resolve());
      ws.once('open', () =>
        send({
          type: 'device.hello',
          deviceId: id,
          name: `name-${id}`,
          version: 'test',
          providers: [
            { provider: 'claude', configuration: CONFIGURATION, capabilities: CAPABILITIES },
          ],
        }),
      );
    });
    return { send, ws };
  };
  const agentA = await connect('dev-a');
  store.mintDevice('dev-d', 'dev-d');

  const until = async (check: () => boolean) => {
    for (let i = 0; i < 100; i++) {
      if (check()) return;
      await new Promise((r) => setTimeout(r, 20));
    }
    throw new Error('did not happen in time');
  };
  const state = async (id: string, s: string) => {
    agentA.send({ type: 'runtime.state', sessionId: id, state: s, capabilities: CAPABILITIES });
    await until(() => control.runtimeOf(id).state === s);
  };
  const settings = async (id: string, patch: Partial<SessionMcpSettings>) => {
    const res = await rest('PUT', `/api/sessions/${id}/mcp`, HOST, {
      settings: { ...DEFAULT_SESSION_MCP_SETTINGS, ...patch },
    });
    expect(res.status).toBe(200);
  };
  const tokenOf = (id: string): string => {
    const minted = control.sessionMcp.mintToken(id);
    if (!minted) throw new Error('no token');
    return minted.secret;
  };
  const propose = (
    token: string,
    objects: Json[],
    reason = 'the project has a convention',
    session = 'a1',
  ) => rest('POST', `/api/sessions/${session}/config-changes`, token, { reason, objects });
  const decide = (changeId: string, what: 'approve' | 'decline' | 'undo', token = HOST) =>
    rest('POST', `/api/config-changes/${changeId}/${what}`, token);
  const audit = () => store.listAudit({}, { order: 'asc', limit: 500 }).events;
  const memory = (content: string, baseHash = '') => ({
    kind: 'memory',
    name: 'project',
    baseHash,
    content,
  });
  const option = (name: string, content: string) => ({ kind: 'config', name, content });
  /** The wire messages the core sent to the browsers' subscribers about a change. */
  const published: Json[] = [];
  control.subscribe((msg) => {
    if (msg.type === 'session.change') published.push(msg as Json);
  });
  return {
    store,
    control,
    clock,
    fake,
    rest,
    state,
    until,
    settings,
    tokenOf,
    propose,
    decide,
    audit,
    memory,
    option,
    published,
    received,
    agentA,
  };
}

/** A booted core whose session a1 may write configuration, with its token. */
async function allowed() {
  const b = await boot();
  await b.settings('a1', { writeConfig: true });
  await b.state('a1', 'ready');
  return { b, token: b.tokenOf('a1') };
}

describe('proposing a change', () => {
  it('is refused without the setting, from the host and with another session token', async () => {
    const b = await boot();
    await b.state('a1', 'ready');
    const token = b.tokenOf('a1');
    const off = await b.propose(token, [b.memory('x')]);
    expect(off.status).toBe(403);
    expect(off.body).toMatchObject({ code: 'forbidden' });
    await b.settings('a1', { writeConfig: true });
    await b.settings('a2', { writeConfig: true });
    expect((await b.propose(HOST, [b.memory('x')])).status).toBe(403);
    const other = await b.propose(b.tokenOf('a2'), [b.memory('x')]);
    expect(other.status).toBe(404);
    expect((await b.propose(token, [b.memory('x')])).status).toBe(201);
    expect(b.fake.calls.preview).toBe(1);
  });

  it('checks the shape of the request', async () => {
    const { b, token } = await allowed();
    expect((await b.propose(token, [])).status).toBe(400);
    expect((await b.propose(token, [b.memory('x')], '')).status).toBe(400);
    expect((await b.propose(token, [{ kind: 'skill', name: '../x', content: 'x' }])).status).toBe(
      400,
    );
    expect(
      (await b.propose(token, [{ kind: 'memory', name: 'project', content: null }])).status,
    ).toBe(400);
    expect((await b.propose(token, [b.option('colour', 'red')])).status).toBe(400);
    const six = Array.from({ length: 6 }, (_, i) => ({
      kind: 'skill',
      name: `s${i}`,
      content: 'x',
    }));
    expect((await b.propose(token, six)).status).toBe(400);
  });

  it('refuses content that is too large or holds a credential, and stores nothing', async () => {
    const { b, token } = await allowed();
    const large = await b.propose(token, [b.memory('x'.repeat(64 * 1024 + 1))]);
    expect(large.body).toMatchObject({ code: 'too-large' });
    const secret = await b.propose(token, [
      b.memory('use -----BEGIN RSA PRIVATE KEY----- to sign'),
    ]);
    expect(secret.status).toBe(422);
    expect(secret.body).toMatchObject({ code: 'unsafe-content', reason: 'memory:project' });
    expect((await b.rest('GET', '/api/sessions/a1/config-changes', token)).body.changes).toEqual(
      [],
    );
    expect(b.audit().filter((e) => e.type === 'config.change.requested')).toHaveLength(0);
    expect(b.fake.calls.preview).toBe(0);
  });

  it('refuses when the owner is offline, the agent cannot preview, or the base is stale', async () => {
    const { b, token } = await allowed();
    await b.settings('d1', { writeConfig: true });
    const offline = await b.propose(b.tokenOf('d1'), [b.memory('x')], 'why', 'd1');
    expect(offline.body).toMatchObject({ code: 'owner-offline' });

    b.fake.behaviour.preview = { ok: false, error: 'unsupported' };
    const unsupported = await b.propose(token, [b.memory('x')]);
    expect(unsupported.status).toBe(501);
    expect(unsupported.body).toMatchObject({ code: 'unsupported' });
    b.fake.behaviour.preview = null;

    b.fake.disk.set('memory:project', 'old');
    const stale = await b.propose(token, [b.memory('new', sha('older'))]);
    expect(stale.status).toBe(409);
    expect(stale.body).toMatchObject({ code: 'stale', reason: 'memory:project' });
    expect((await b.propose(token, [b.memory('new')])).body).toMatchObject({ code: 'stale' });
    expect((await b.propose(token, [b.memory('new', sha('old'))])).status).toBe(201);
  });

  it('refuses options the driver does not allow a session to set', async () => {
    const { b, token } = await allowed();
    expect((await b.propose(token, [b.option('effort', 'high')])).body).toMatchObject({
      code: 'forbidden',
      reason: 'unknown-option',
    });
    expect((await b.propose(token, [b.option('model', 'gpt')])).body).toMatchObject({
      reason: 'unknown-value',
    });
    expect((await b.propose(token, [b.option('model', 'unlimited')])).body).toMatchObject({
      reason: 'high-risk',
    });
    // `mode` is not one of the closed names, so the shape check refuses it before the driver is asked.
    expect((await b.propose(token, [b.option('mode', 'bypass')])).status).toBe(400);
    expect((await b.propose(token, [b.option('model', 'opus')])).status).toBe(201);
  });

  it('keeps one open request per session and audits the request without its content', async () => {
    const { b, token } = await allowed();
    const first = await b.propose(token, [b.memory('SECRET-FREE TEXT'), b.option('model', 'opus')]);
    expect(first.status).toBe(201);
    expect(first.body).toMatchObject({
      sessionId: 'a1',
      state: 'pending',
      undoable: false,
      objects: [
        { kind: 'memory', name: 'project', baseHash: '', newHash: sha('SECRET-FREE TEXT') },
        { kind: 'config', name: 'model', content: 'opus', diff: '-model: sonnet\n+model: opus\n' },
      ],
    });
    expect(first.body.expiresAt - first.body.createdAt).toBe(DAY);
    expect(first.body.previous).toBeUndefined();
    expect((await b.propose(token, [b.memory('y')])).body).toMatchObject({
      code: 'forbidden',
      reason: 'request-open',
    });
    const records = b.audit().filter((e) => e.type === 'config.change.requested');
    expect(records).toHaveLength(1);
    expect(records[0]).toMatchObject({ actorKind: 'session', actorId: 'a1', sessionId: 'a1' });
    expect(JSON.stringify(records[0])).not.toContain('SECRET-FREE TEXT');
    expect(JSON.stringify(records[0])).toContain('memory:project');
    await b.until(() => b.published.length === 1);
    expect(b.published[0]).toMatchObject({
      sessionId: 'a1',
      changeId: first.body.changeId,
      state: 'pending',
    });
    expect(b.fake.calls.apply).toHaveLength(0);
  });
});

describe('reading changes', () => {
  it('lists and gets a session its own, and the host all', async () => {
    const { b, token } = await allowed();
    const made = await b.propose(token, [b.memory('x')]);
    const list = await b.rest('GET', '/api/sessions/a1/config-changes', token);
    expect(list.status).toBe(200);
    expect(list.body.changes.map((c: Json) => c.changeId)).toEqual([made.body.changeId]);
    expect((await b.rest('GET', `/api/config-changes/${made.body.changeId}`, token)).status).toBe(
      200,
    );
    expect((await b.rest('GET', `/api/config-changes/${made.body.changeId}`, HOST)).status).toBe(
      200,
    );
    expect((await b.rest('GET', '/api/config-changes/nope', token)).status).toBe(404);
    await b.settings('a2', { writeConfig: true });
    const a2 = b.tokenOf('a2');
    expect((await b.rest('GET', `/api/config-changes/${made.body.changeId}`, a2)).status).toBe(404);
    expect((await b.rest('GET', '/api/sessions/a1/config-changes', a2)).status).toBe(404);
  });

  it('is not readable without the read setting', async () => {
    const { b, token } = await allowed();
    await b.settings('a1', { writeConfig: false, readConfig: false });
    expect((await b.rest('GET', '/api/sessions/a1/config-changes', token)).status).toBe(403);
  });
});

describe('the user decides', () => {
  it('only the host decides, and a decline closes the request and frees the session', async () => {
    const { b, token } = await allowed();
    const made = await b.propose(token, [b.memory('x')]);
    const id = made.body.changeId;
    expect((await b.decide(id, 'approve', token)).status).toBe(403);
    expect((await b.decide(id, 'decline', token)).status).toBe(403);
    const declined = await b.decide(id, 'decline');
    expect(declined.body).toMatchObject({ state: 'declined', undoable: false });
    expect(declined.body.decidedAt).toBeTypeOf('number');
    expect((await b.decide(id, 'approve')).body).toMatchObject({ code: 'request-closed' });
    expect((await b.decide('nope', 'approve')).body).toMatchObject({ code: 'not-found' });
    expect(b.fake.calls.apply).toHaveLength(0);
    const decided = b.audit().filter((e) => e.type === 'config.change.decided');
    expect(decided).toHaveLength(1);
    expect(decided[0]).toMatchObject({ actorKind: 'operator' });
    expect((await b.propose(token, [b.memory('y')])).status).toBe(201);
  });

  it('writes the files and sets the options when approved, and is audited', async () => {
    const { b, token } = await allowed();
    b.fake.disk.set('memory:project', 'old');
    const made = await b.propose(token, [b.memory('new', sha('old')), b.option('model', 'opus')]);
    const approved = await b.decide(made.body.changeId, 'approve');
    expect(approved.status).toBe(200);
    expect(['approved', 'applied']).toContain(approved.body.state);
    await b.until(() => b.control.configChanges.get(made.body.changeId)?.state === 'applied');
    expect(b.fake.disk.get('memory:project')).toBe('new');
    expect(b.fake.calls.apply).toHaveLength(1);
    expect(b.fake.calls.apply[0]?.objects).toEqual([
      {
        kind: 'memory',
        name: 'project',
        content: 'new',
        baseHash: sha('old'),
        newHash: sha('new'),
      },
    ]);
    await b.until(() =>
      b.received.some((m) => m.type === 'runtime.config' && m.configId === 'model'),
    );
    expect(b.received.find((m) => m.type === 'runtime.config')).toMatchObject({
      sessionId: 'a1',
      configId: 'model',
      value: 'opus',
    });
    const got = await b.rest('GET', `/api/config-changes/${made.body.changeId}`, token);
    expect(got.body).toMatchObject({ state: 'applied', undoable: true });
    expect(got.body.appliedAt).toBeTypeOf('number');
    const types = b.audit().map((e) => e.type);
    expect(types).toContain('config.change.requested');
    expect(types).toContain('config.change.decided');
    expect(types).toContain('config.change.applied');
    await b.until(() => b.published.some((m) => m.state === 'applied'));
    expect(b.published.map((m) => m.state)).toEqual(['pending', 'approved', 'applied']);
  });

  it('fails and puts the options back when the files cannot be written', async () => {
    const { b, token } = await allowed();
    const made = await b.propose(token, [b.memory('new'), b.option('model', 'opus')]);
    b.fake.behaviour.apply = { ok: false, error: 'failed', reason: 'disk full' };
    await b.decide(made.body.changeId, 'approve');
    await b.until(() => b.control.configChanges.get(made.body.changeId)?.state === 'failed');
    const failed = b.control.configChanges.get(made.body.changeId);
    expect(failed).toMatchObject({ error: 'failed', errorReason: 'disk full', undoable: false });
    const sent = b.received.filter((m) => m.type === 'runtime.config');
    expect(sent.map((m) => m.value)).toEqual(['opus', 'sonnet']);
    expect(b.audit().filter((e) => e.type === 'config.change.failed')).toHaveLength(1);
    expect((await b.propose(token, [b.memory('again')])).status).toBe(201);
  });

  it('stays approved while the owner is offline and is written when it connects', async () => {
    const { b, token } = await allowed();
    const made = await b.propose(token, [b.memory('new')]);
    b.agentA.ws.close();
    await b.until(
      () => b.control.devices().find((d) => d.deviceId === 'dev-a')?.connected === false,
    );
    const approved = await b.decide(made.body.changeId, 'approve');
    expect(approved.body.state).toBe('approved');
    expect(b.fake.calls.apply).toHaveLength(0);
    expect(approved.body.expiresAt - approved.body.decidedAt).toBe(DAY);
    // The owner connects again.
    const device = b.store.mintDevice('dev-a', 'dev-a');
    const ws = new WebSocket(
      `ws://127.0.0.1:${((servers[0] as Server).address() as AddressInfo).port}/ws/agent`,
      { headers: { authorization: `Bearer ${device.token}` } },
    );
    sockets.push(ws);
    ws.once('open', () =>
      ws.send(
        JSON.stringify({
          v: PROTOCOL_VERSION,
          type: 'device.hello',
          deviceId: 'dev-a',
          name: 'name-dev-a',
          version: 'test',
          providers: [
            { provider: 'claude', configuration: CONFIGURATION, capabilities: CAPABILITIES },
          ],
        }),
      ),
    );
    await b.until(() => b.control.configChanges.get(made.body.changeId)?.state === 'applied');
    expect(b.fake.disk.get('memory:project')).toBe('new');
  });
});

describe('a request that goes away', () => {
  it('expires after 24 hours', async () => {
    const { b, token } = await allowed();
    const made = await b.propose(token, [b.memory('x')]);
    b.clock.now += DAY + 1;
    expect((await b.decide(made.body.changeId, 'approve')).body).toMatchObject({
      code: 'request-closed',
    });
    expect(b.control.configChanges.get(made.body.changeId)).toMatchObject({ state: 'expired' });
    expect(b.fake.calls.apply).toHaveLength(0);
  });

  it('expires when its session ends', async () => {
    const { b, token } = await allowed();
    const made = await b.propose(token, [b.memory('x')]);
    await b.state('a1', 'running');
    await b.state('a1', 'ready');
    await b.state('a1', 'idle');
    await b.until(() => b.control.configChanges.get(made.body.changeId)?.state === 'expired');
    expect((await b.decide(made.body.changeId, 'approve')).body).toMatchObject({
      code: 'request-closed',
    });
  });

  it('is forgotten with its session', async () => {
    const { b, token } = await allowed();
    const made = await b.propose(token, [b.memory('x')]);
    b.store.forgetSessionMcp('a1');
    expect(b.store.configChange(made.body.changeId)).toBeNull();
  });
});

describe('undoing an applied change', () => {
  async function applied(content = 'new') {
    const { b, token } = await allowed();
    b.fake.disk.set('memory:project', 'old');
    const made = await b.propose(token, [b.memory(content, sha('old')), b.option('model', 'opus')]);
    await b.decide(made.body.changeId, 'approve');
    await b.until(() => b.control.configChanges.get(made.body.changeId)?.state === 'applied');
    return { b, token, id: made.body.changeId as string };
  }

  it('writes the previous content back and the previous option, once', async () => {
    const { b, id } = await applied();
    const undone = await b.decide(id, 'undo');
    expect(undone.status).toBe(200);
    expect(undone.body).toMatchObject({ state: 'undone', undoable: false });
    expect(undone.body.undoneAt).toBeTypeOf('number');
    expect(b.fake.disk.get('memory:project')).toBe('old');
    expect(b.fake.calls.apply[1]?.objects).toEqual([
      {
        kind: 'memory',
        name: 'project',
        content: 'old',
        baseHash: sha('new'),
        newHash: sha('old'),
      },
    ]);
    await b.until(
      () =>
        b.received.filter((m) => m.type === 'runtime.config' && m.value === 'sonnet').length > 0,
    );
    expect(b.audit().filter((e) => e.type === 'config.change.undone')).toHaveLength(1);
    expect((await b.decide(id, 'undo')).body).toMatchObject({ code: 'request-closed' });
  });

  it('deletes a file the change created', async () => {
    const { b, token } = await allowed();
    const made = await b.propose(token, [{ kind: 'skill', name: 'review', content: 'steps' }]);
    await b.decide(made.body.changeId, 'approve');
    await b.until(() => b.control.configChanges.get(made.body.changeId)?.state === 'applied');
    expect(b.fake.disk.get('skill:review')).toBe('steps');
    expect((await b.decide(made.body.changeId, 'undo')).status).toBe(200);
    expect(b.fake.disk.has('skill:review')).toBe(false);
    expect(b.fake.calls.apply[1]?.objects[0]).toMatchObject({ content: null, newHash: '' });
  });

  it('stays applied and says why when the file changed since', async () => {
    const { b, id } = await applied();
    b.fake.disk.set('memory:project', 'edited by the user');
    const refused = await b.decide(id, 'undo');
    expect(refused.status).toBe(409);
    expect(refused.body).toMatchObject({ code: 'stale' });
    expect(b.control.configChanges.get(id)).toMatchObject({
      state: 'applied',
      error: 'stale',
      undoable: true,
    });
    expect(b.audit().filter((e) => e.type === 'config.change.failed')).toHaveLength(1);
    expect(b.fake.disk.get('memory:project')).toBe('edited by the user');
  });

  it('answers owner-offline without recording anything', async () => {
    const { b, id } = await applied();
    b.agentA.ws.close();
    await b.until(
      () => b.control.devices().find((d) => d.deviceId === 'dev-a')?.connected === false,
    );
    expect((await b.decide(id, 'undo')).body).toMatchObject({ code: 'owner-offline' });
    expect(b.control.configChanges.get(id)).toMatchObject({ state: 'applied' });
    expect(b.audit().filter((e) => e.type === 'config.change.failed')).toHaveLength(0);
  });

  it('is only for applied changes that still hold the previous content', async () => {
    const { b, token } = await allowed();
    const made = await b.propose(token, [b.memory('x')]);
    expect((await b.decide(made.body.changeId, 'undo')).body).toMatchObject({
      code: 'request-closed',
    });
    expect((await b.decide('nope', 'undo')).body).toMatchObject({ code: 'not-found' });
    expect((await b.decide(made.body.changeId, 'undo', token)).status).toBe(403);
  });

  it('loses the previous content after 30 days, and the record stays', async () => {
    const { b, id } = await applied();
    expect(b.control.configChanges.prune()).toBe(0);
    b.clock.now += 29 * DAY;
    expect(b.control.configChanges.prune()).toBe(0);
    b.clock.now += 2 * DAY;
    expect(b.control.configChanges.prune()).toBe(1);
    expect(b.control.configChanges.get(id)).toMatchObject({ state: 'applied', undoable: false });
    expect((await b.decide(id, 'undo')).body).toMatchObject({ code: 'request-closed' });
    expect(b.control.configChanges.prune()).toBe(0);
  });
});

describe('tokens', () => {
  it('cannot be created with the session-only capabilities', async () => {
    const b = await boot();
    for (const capability of ['read-config', 'write-config']) {
      const res = await b.rest('POST', '/api/tokens', HOST, {
        name: 'x',
        grant: { capabilities: [capability] },
        expiresInDays: 5,
      });
      expect(res.status).toBe(400);
    }
  });
});
