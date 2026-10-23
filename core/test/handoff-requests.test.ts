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
import { createControl, type SessionLimits } from '../src/control/index.ts';
import { attachIngest } from '../src/ingest/index.ts';
import { Store } from '../src/store/index.ts';

// biome-ignore lint/suspicious/noExplicitAny: parsed wire JSON, read by field name
type Json = Record<string, any>;

const HOST = 'host-token-0123456789abcdef';

const dirs: string[] = [];
const servers: Server[] = [];
const sockets: WebSocket[] = [];

afterEach(async () => {
  for (const ws of sockets.splice(0)) ws.terminate();
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

const CONFIGURATION = {
  id: 'claude',
  name: 'Claude Code',
  extends: 'claude',
  enabled: true,
  env: [],
  defaults: {},
};
const CAPABILITIES = { permissions: true, interrupt: true, config: [] };

/** Sessions a1 and a2 on machine A; B and C are connected, D is stored but offline. */
async function boot(limits: Partial<SessionLimits> = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-handoff-requests-'));
  dirs.push(dir);
  const store = new Store(dir);
  for (const id of ['a1', 'a2']) {
    store.append(
      {
        sessionId: id,
        provider: 'claude',
        projectDir: 'p',
        filePath: `/x/${id}`,
        deviceId: 'dev-a',
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
  const control = createControl(store, {
    promptTimeoutMs: 600,
    workspaceTimeoutMs: 150,
    handoffTimeoutMs: 300,
    sessionLimits: limits,
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
    return {
      status: res.status,
      headers: res.headers,
      body: (text ? JSON.parse(text) : null) as Json,
    };
  };

  const connect = async (id: string) => {
    const device = store.mintDevice(id, id);
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/agent`, {
      headers: { authorization: `Bearer ${device.token}` },
    });
    sockets.push(ws);
    const send = (msg: Json) => ws.send(JSON.stringify({ v: PROTOCOL_VERSION, ...msg }));
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
    return { send };
  };
  const agentA = await connect('dev-a');
  await connect('dev-b');
  await connect('dev-c');
  store.mintDevice('dev-d', 'dev-d');

  const state = (id: string, s: string) =>
    agentA.send({ type: 'runtime.state', sessionId: id, state: s, capabilities: CAPABILITIES });
  const until = async (check: () => boolean) => {
    for (let i = 0; i < 100; i++) {
      if (check()) return;
      await new Promise((r) => setTimeout(r, 20));
    }
    throw new Error('did not happen in time');
  };
  const running = async (id: string) => {
    state(id, 'running');
    await until(() => control.runtimeOf(id).state === 'running');
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
  const ask = (token: string, target = 'dev-b', reason = 'needs the GPU') =>
    rest('POST', '/api/handoff-requests', token, { targetDeviceId: target, reason });
  const decide = (requestId: string, what: 'approve' | 'decline') =>
    rest('POST', `/api/handoff-requests/${requestId}/${what}`, HOST);
  const audit = () => store.listAudit({}, { order: 'asc', limit: 500 }).events;
  return { store, control, rest, state, until, running, settings, tokenOf, ask, decide, audit };
}

describe('asking for a hand-off', () => {
  it('is refused without the setting, from the host and for an unreachable machine', async () => {
    const b = await boot();
    const token = b.tokenOf('a1');
    const first = await b.ask(token);
    expect(first.status).toBe(403);
    expect(first.body).toMatchObject({ code: 'forbidden' });
    expect((await b.ask(HOST)).status).toBe(403);
    await b.settings('a1', { requestHandoff: true, visibility: 'all' });
    expect((await b.ask(token, 'dev-a')).body).toMatchObject({ code: 'same-device' });
    expect((await b.ask(token, 'nowhere')).body).toMatchObject({ code: 'not-found' });
    expect((await b.ask(token, 'dev-d')).body).toMatchObject({ code: 'target-offline' });
    expect((await b.ask(token, 'dev-b', '')).status).toBe(400);
    expect((await b.ask(token, 'dev-b', 'x'.repeat(501))).status).toBe(400);
    expect(b.audit().filter((e) => e.type === 'handoff.requested')).toHaveLength(0);
  });

  it('waits for the user, one request at a time, and is audited', async () => {
    const b = await boot();
    await b.settings('a1', { requestHandoff: true, visibility: 'all' });
    const token = b.tokenOf('a1');
    const emitted: unknown[] = [];
    b.control.onEvent((e) => {
      if (e.kind === 'handoff-requested') emitted.push(e);
    });
    const res = await b.ask(token);
    expect(res.status).toBe(201);
    expect(emitted).toEqual([
      {
        kind: 'handoff-requested',
        sessionId: 'a1',
        requestId: res.body.requestId,
        targetName: 'name-dev-b',
      },
    ]);
    expect(res.body).toMatchObject({
      sessionId: 'a1',
      targetDeviceId: 'dev-b',
      reason: 'needs the GPU',
      state: 'pending',
      decision: null,
    });
    expect(res.body.expiresAt - res.body.createdAt).toBe(24 * 3_600_000);
    expect((await b.ask(token, 'dev-c')).body).toMatchObject({
      code: 'forbidden',
      reason: 'request-open',
    });

    const list = await b.rest('GET', '/api/sessions/a1/handoff-requests', HOST);
    expect(list.body.requests).toHaveLength(1);
    expect((await b.rest('GET', '/api/sessions/a1/handoff-requests', token)).status).toBe(403);
    expect((await b.decide(res.body.requestId, 'approve')).status).toBe(200);
    const asDevice = await b.rest(
      'POST',
      `/api/handoff-requests/${res.body.requestId}/decline`,
      token,
    );
    expect(asDevice.status).toBe(403);

    const events = b.audit().filter((e) => e.type === 'handoff.requested');
    expect(events).toHaveLength(1);
    expect(events[0]).toMatchObject({ actorKind: 'session', actorId: 'a1', sessionId: 'a1' });
  });

  it('is declined by the user, who is recorded, and leaves room for the next request', async () => {
    const b = await boot();
    await b.settings('a1', { requestHandoff: true, visibility: 'all' });
    const made = await b.ask(b.tokenOf('a1'));
    const res = await b.decide(made.body.requestId, 'decline');
    expect(res.status).toBe(200);
    expect(res.body).toMatchObject({ state: 'declined', decision: 'user' });
    expect((await b.decide(made.body.requestId, 'approve')).body).toMatchObject({
      code: 'request-closed',
    });
    expect((await b.decide('nope', 'approve')).body).toMatchObject({ code: 'not-found' });
    const decided = b.audit().filter((e) => e.type === 'handoff.decided');
    expect(decided).toHaveLength(1);
    expect(decided[0]).toMatchObject({ actorKind: 'operator' });
    expect((await b.ask(b.tokenOf('a1'), 'dev-c')).status).toBe(201);
  });

  it('is limited per hour', async () => {
    const b = await boot({ handoffRequestsPerHour: 2 });
    await b.settings('a1', { requestHandoff: true, visibility: 'all' });
    const token = b.tokenOf('a1');
    for (let i = 0; i < 2; i++) {
      const made = await b.ask(token);
      expect(made.status).toBe(201);
      await b.decide(made.body.requestId, 'decline');
    }
    const res = await b.ask(token);
    expect(res.status).toBe(429);
    expect(res.body.code).toBe('rate-limited');
    expect(Number(res.headers.get('retry-after'))).toBeGreaterThan(0);
  });
});

describe('an approved request', () => {
  it('waits for the end of the turn, then runs and records why it failed', async () => {
    const b = await boot();
    await b.settings('a1', { requestHandoff: true, visibility: 'all' });
    await b.running('a1');
    const made = await b.ask(b.tokenOf('a1'));
    const approved = await b.decide(made.body.requestId, 'approve');
    expect(approved.body).toMatchObject({ state: 'approved', decision: 'user' });
    await new Promise((r) => setTimeout(r, 100));
    expect(b.store.handoffRequest(made.body.requestId)?.state).toBe('approved');

    // The fake agent has no snapshot to give, so the hand-off ends and the request carries its error.
    b.state('a1', 'ready');
    await b.until(() => b.store.handoffRequest(made.body.requestId)?.state === 'failed');
    expect(b.store.handoffRequest(made.body.requestId)?.error).toBeTruthy();
    const decided = b.audit().filter((e) => e.type === 'handoff.decided');
    expect(decided[0]).toMatchObject({ actorKind: 'operator' });
  });
});

describe('automatic approval', () => {
  const auto = {
    requestHandoff: true,
    visibility: 'all' as const,
    autoApproveHandoff: true,
    autoApproveMachines: ['dev-b'],
  };

  it('needs the setting and a machine on the list, and is recorded as auto', async () => {
    const b = await boot();
    await b.settings('a1', auto);
    await b.running('a1');
    const listed = await b.ask(b.tokenOf('a1'), 'dev-b');
    expect(listed.body).toMatchObject({ state: 'approved', decision: 'auto' });
    const decided = b.audit().filter((e) => e.type === 'handoff.decided');
    expect(decided[0]).toMatchObject({ actorKind: 'core' });

    await b.settings('a2', auto);
    const other = await b.ask(b.tokenOf('a2'), 'dev-c');
    expect(other.body).toMatchObject({ state: 'pending', decision: null });
  });

  it('allows three an hour per session, then asks the user', async () => {
    const b = await boot({ autoApprovedHandoffsPerHour: 3, handoffRequestsPerHour: 10 });
    await b.settings('a1', auto);
    await b.running('a1');
    const token = b.tokenOf('a1');
    for (let i = 0; i < 3; i++) {
      const made = await b.ask(token);
      expect(made.body).toMatchObject({ state: 'approved', decision: 'auto' });
      // The session stays in its turn; close the request to leave room for the next one.
      b.store.changeHandoffRequest(made.body.requestId, ['approved'], { state: 'expired' }, null);
    }
    expect((await b.ask(token)).body).toMatchObject({ state: 'pending', decision: null });
  });
});

describe('a request that goes away', () => {
  it('expires when its session ends', async () => {
    const b = await boot();
    await b.settings('a1', { requestHandoff: true, visibility: 'all' });
    const made = await b.ask(b.tokenOf('a1'));
    b.state('a1', 'ready');
    await b.until(() => b.control.runtimeOf('a1').state === 'ready');
    b.state('a1', 'idle');
    await b.until(() => b.store.handoffRequest(made.body.requestId)?.state === 'expired');
    expect((await b.decide(made.body.requestId, 'approve')).body).toMatchObject({
      code: 'request-closed',
    });
  });

  it('is forgotten with its session', async () => {
    const b = await boot();
    await b.settings('a1', { requestHandoff: true, visibility: 'all' });
    const made = await b.ask(b.tokenOf('a1'));
    b.store.forgetSessionMcp('a1');
    expect(b.store.handoffRequest(made.body.requestId)).toBeNull();
  });
});
