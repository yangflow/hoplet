import { createHmac } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync, statSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  type HookCreateRequest,
  type HookEnvelope,
  HookEnvelopeSchema,
  PROTOCOL_VERSION,
  type ServerMessage,
  safeParse,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { createApi } from '../src/api/index.ts';
import {
  type Control,
  type ControlEvent,
  type ControlReply,
  createControl,
  type DeviceLink,
} from '../src/control/index.ts';
import {
  classifyAddress,
  createHooks,
  createPinnedFetch,
  type Hooks,
  refuseUrl,
} from '../src/hooks/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
const servers: Server[] = [];
const stops: Array<() => void> = [];

afterEach(async () => {
  for (const stop of stops.splice(0)) stop();
  for (const s of servers.splice(0)) {
    s.closeAllConnections();
    await new Promise<void>((r) => s.close(() => r()));
  }
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

// biome-ignore lint/suspicious/noExplicitAny: parsed JSON of a response, read loosely in assertions
type Json = Record<string, any>;

const T0 = Date.UTC(2026, 9, 16, 12, 0);
const PROJECT = '/work/app';
const URL_A = 'https://hooks.example/a?token=abc';
const PUBLIC = async () => ['93.184.216.34'];

interface Call {
  url: string;
  headers: Headers;
  body: string;
}

/** A stand-in for the transport: records every request and answers with the next scripted status. */
function stubFetch(statuses: Array<number | 'error'> = [200]) {
  const calls: Call[] = [];
  const impl = (async (input: string | URL | Request, init?: RequestInit) => {
    calls.push({
      url: String(input),
      headers: new Headers(init?.headers),
      body: String(init?.body ?? ''),
    });
    const status = statuses[Math.min(calls.length - 1, statuses.length - 1)] ?? 200;
    if (status === 'error') throw new TypeError('fetch failed');
    return new Response(null, { status });
  }) as typeof fetch;
  return { calls, impl };
}

function fakeLink(): DeviceLink & { sent: ServerMessage[] } {
  const link = {
    sent: [] as ServerMessage[],
    send(msg: ServerMessage) {
      link.sent.push(msg);
    },
    async sendAndWait(msg: ServerMessage) {
      link.sent.push(msg);
    },
    isOpen: () => true,
  };
  return link;
}

const state = (sessionId: string, value: string, error?: string) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'runtime.state',
    sessionId,
    state: value,
    ...(error ? { error } : {}),
    capabilities: { permissions: true, interrupt: true, config: [] },
  }) as ControlReply;

const permission = (requestId: string, tool: string, input: unknown) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'runtime.permission',
    sessionId: 's1',
    requestId,
    tool,
    input,
  }) as ControlReply;

function setup(options: { fetch?: typeof fetch; allowPrivate?: boolean } = {}) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-hooks-'));
  dirs.push(dir);
  const store = new Store(dir);
  store.append(
    { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'dev-a' },
    [
      JSON.stringify({
        type: 'user',
        cwd: PROJECT,
        message: { role: 'user', content: 'Fix the login bug' },
      }),
    ],
  );
  const clock = { now: T0 };
  const control = createControl(store, { now: () => clock.now, startTimeoutMs: 100 });
  const link = fakeLink();
  control.attach('dev-a', link);
  // A bus for the moments that are awkward to provoke through a fake device.
  const listeners: Array<(e: ControlEvent) => void> = [];
  const bus = {
    onEvent(l: (e: ControlEvent) => void) {
      listeners.push(l);
      return () => listeners.splice(listeners.indexOf(l), 1);
    },
  } as unknown as Control;
  const emit = (e: ControlEvent) => {
    for (const l of [...listeners]) l(e);
  };
  const fetched = options.fetch ? null : stubFetch();
  const make = (c: Control): Hooks => {
    const hooks = createHooks(store, c, {
      now: () => clock.now,
      fetch: options.fetch ?? (fetched as ReturnType<typeof stubFetch>).impl,
      resolve: PUBLIC,
      allowPrivate: options.allowPrivate ?? false,
    });
    hooks.start();
    stops.push(() => hooks.stop());
    return hooks;
  };
  return { dir, store, control, link, clock, emit, make, bus, fetched };
}

async function withHook(
  hooks: Hooks,
  over: Partial<HookCreateRequest> = {},
): Promise<{ id: string; secret: string }> {
  const created = await hooks.create({
    url: URL_A,
    label: 'Chat',
    events: ['session.started', 'turn.completed', 'session.ended', 'permission.requested'],
    ...over,
  });
  if (!created.ok) throw new Error(`create failed: ${created.error}`);
  return { id: created.hook.id, secret: created.secret };
}

const ALL = [
  'session.started',
  'turn.completed',
  'session.ended',
  'permission.requested',
  'permission.resolved',
  'session.error',
  'handoff.started',
  'handoff.completed',
] as const;

const bodyOf = (c: Call): HookEnvelope => JSON.parse(c.body) as HookEnvelope;

/** Lets the sender run what the event queued. */
async function settle(hooks: Hooks): Promise<void> {
  await new Promise((r) => setImmediate(r));
  hooks.pump();
  await hooks.idle();
}

describe('events at every lifecycle point', () => {
  async function all() {
    const f = setup();
    const hooks = f.make(f.bus);
    await withHook(hooks, { events: [...ALL], summary: true });
    const sent = f.fetched as ReturnType<typeof stubFetch>;
    const fire = async (e: ControlEvent) => {
      const before = sent.calls.length;
      f.emit(e);
      await settle(hooks);
      expect(sent.calls.length).toBe(before + 1);
      const env = bodyOf(sent.calls[before] as Call);
      expect(safeParse(HookEnvelopeSchema, env).success).toBe(true);
      return env;
    };
    return { ...f, hooks, sent, fire };
  }

  it('session.started names the provider, the machine and the project', async () => {
    const a = await all();
    const env = await a.fire({
      kind: 'started',
      sessionId: 's1',
      deviceId: 'dev-a',
      projectPath: PROJECT,
      provider: 'claude',
      origin: 'new',
    });
    expect(env).toMatchObject({
      v: 1,
      type: 'session.started',
      session: { id: 's1', project: { name: 'app' }, machine: { id: 'dev-a' } },
      actor: { kind: 'user' },
      data: { provider: 'claude', origin: 'new' },
    });
    expect(env.id).toMatch(/^evt_[0-9A-HJKMNP-TV-Z]{26}$/);
    expect(env.at).toBe(new Date(T0).toISOString());
    expect(a.sent.calls[0]?.headers.get('hoplet-event')).toBe('session.started');
  });

  it('turn.completed carries the turn count, the duration and, with summary, the title', async () => {
    const a = await all();
    const env = await a.fire({ kind: 'turn-done', sessionId: 's1', turn: 3, durationMs: 4200 });
    expect(env).toMatchObject({
      type: 'turn.completed',
      actor: { kind: 'system' },
      data: { turn: 3, durationMs: 4200, title: 'Fix the login bug' },
    });
  });

  it('session.ended tells why: stopped, idle or deleted', async () => {
    const a = await all();
    const stopped = await a.fire({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    expect(stopped).toMatchObject({ type: 'session.ended', data: { reason: 'stopped' } });
    expect(stopped.actor).toEqual({ kind: 'user' });
    const idle = await a.fire({ kind: 'ended', sessionId: 's1', reason: 'idle' });
    expect(idle.actor).toEqual({ kind: 'system' });
    const deleted = await a.fire({
      kind: 'ended',
      sessionId: 's1',
      reason: 'deleted',
      deviceId: 'dev-a',
      projectPath: PROJECT,
    });
    expect(deleted).toMatchObject({
      data: { reason: 'deleted' },
      session: { project: { name: 'app' }, machine: { id: 'dev-a' } },
    });
  });

  it('permission.requested has a redacted, clipped summary only when asked for', async () => {
    const a = await all();
    const env = await a.fire({
      kind: 'permission',
      sessionId: 's1',
      requestId: 'r1',
      tool: 'Bash',
      input: { command: 'curl -H "Authorization: Bearer abcdef0123456789" x.test' },
      plan: false,
      expiresAt: T0 + 600_000,
    });
    expect(env).toMatchObject({
      type: 'permission.requested',
      data: { requestId: 'r1', tool: 'Bash', plan: false, expiresAt: T0 + 600_000 },
    });
    const summary = (env.data as { summary?: string }).summary ?? '';
    expect(summary).not.toContain('abcdef0123456789');
    expect([...summary].length).toBeLessThanOrEqual(80);
  });

  it('permission.resolved names the resolution and who resolved it', async () => {
    const a = await all();
    for (const resolution of ['allow', 'deny', 'expired', 'abandoned'] as const) {
      const env = await a.fire({
        kind: 'resolved',
        sessionId: 's1',
        requestId: 'r1',
        resolution,
        actor: resolution === 'allow' ? { kind: 'action-link' } : { kind: 'rule', id: 'rule-1' },
      });
      expect(env).toMatchObject({
        type: 'permission.resolved',
        data: { requestId: 'r1', resolution },
      });
      expect(env.actor).toEqual(
        resolution === 'allow' ? { kind: 'action-link' } : { kind: 'rule', id: 'rule-1' },
      );
    }
  });

  it('session.error covers a failed start, a runtime error and a failed handoff', async () => {
    const a = await all();
    const start = await a.fire({
      kind: 'failure',
      source: 'start',
      projectPath: PROJECT,
      deviceId: 'dev-a',
      error: 'device-offline',
    });
    expect(start).toMatchObject({
      type: 'session.error',
      session: { project: { name: 'app' }, machine: { id: 'dev-a' } },
      data: { stage: 'start', code: 'device-offline' },
    });
    expect(start.session.id).toBeUndefined();
    const runtime = await a.fire({
      kind: 'runtime-error',
      sessionId: 's1',
      code: 'runtime-failed',
      reason: 'x'.repeat(500),
    });
    expect(runtime).toMatchObject({ data: { stage: 'runtime', code: 'runtime-failed' } });
    expect([...((runtime.data as { reason?: string }).reason ?? '')].length).toBeLessThanOrEqual(
      200,
    );
    const handoff = await a.fire({
      kind: 'failure',
      source: 'handoff',
      sessionId: 's1',
      error: 'clone-failed',
    });
    expect(handoff).toMatchObject({ data: { stage: 'handoff', code: 'clone-failed' } });
  });

  it('handoff.started and handoff.completed name both machines', async () => {
    const a = await all();
    const started = await a.fire({
      kind: 'handoff-started',
      sessionId: 's1',
      handoffId: 'h1',
      from: 'dev-a',
      to: 'dev-b',
    });
    expect(started).toMatchObject({
      type: 'handoff.started',
      session: { machine: { id: 'dev-a' } },
      data: { from: { id: 'dev-a' }, to: { id: 'dev-b' } },
    });
    const done = await a.fire({
      kind: 'handoff-done',
      sessionId: 's1',
      handoffId: 'h1',
      from: 'dev-a',
      to: 'dev-b',
    });
    expect(done).toMatchObject({
      type: 'handoff.completed',
      data: { to: { id: 'dev-b' } },
    });
  });

  it('follows a real control: permission, answer, turn done and the end of the process', async () => {
    const f = setup();
    const hooks = f.make(f.control);
    await withHook(hooks, { events: [...ALL] });
    const sent = f.fetched as ReturnType<typeof stubFetch>;
    f.control.receive(f.link, state('s1', 'running'));
    f.control.receive(f.link, permission('r1', 'Edit', { file_path: `${PROJECT}/a.ts` }));
    await settle(hooks);
    f.control.answerPermission('s1', 'dev-a', 'r1', { decision: 'allow' });
    await settle(hooks);
    f.clock.now += 2000;
    f.control.receive(f.link, state('s1', 'ready'));
    f.control.receive(f.link, state('s1', 'idle'));
    await settle(hooks);
    const types = sent.calls.map((c) => bodyOf(c).type);
    expect(types).toEqual([
      'permission.requested',
      'permission.resolved',
      'turn.completed',
      'session.ended',
    ]);
    expect(bodyOf(sent.calls[1] as Call).data).toMatchObject({ resolution: 'allow' });
    expect(bodyOf(sent.calls[2] as Call).data).toMatchObject({ turn: 1, durationMs: 2000 });
    expect(bodyOf(sent.calls[3] as Call).data).toMatchObject({ reason: 'idle' });
  });

  it('reports a runtime that failed', async () => {
    const f = setup();
    const hooks = f.make(f.control);
    await withHook(hooks, { events: ['session.error'], summary: true });
    const sent = f.fetched as ReturnType<typeof stubFetch>;
    f.control.receive(f.link, state('s1', 'running'));
    f.control.receive(f.link, state('s1', 'failed', 'boom'));
    await settle(hooks);
    expect(sent.calls).toHaveLength(1);
    expect(bodyOf(sent.calls[0] as Call).data).toMatchObject({
      stage: 'runtime',
      code: 'runtime-failed',
      reason: 'boom',
    });
  });
});

describe('filtering', () => {
  it('sends a hook only the events it chose', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    await withHook(hooks, { events: ['session.ended'] });
    f.emit({ kind: 'turn-done', sessionId: 's1', turn: 1, durationMs: 1 });
    f.emit({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    await settle(hooks);
    const sent = f.fetched as ReturnType<typeof stubFetch>;
    expect(sent.calls.map((c) => bodyOf(c).type)).toEqual(['session.ended']);
  });

  it('sends a project-scoped hook only that machine and project', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    await withHook(hooks, {
      events: ['turn.completed'],
      scope: { kind: 'project', deviceId: 'dev-a', projectPath: PROJECT },
    });
    await withHook(hooks, {
      label: 'other',
      events: ['turn.completed'],
      scope: { kind: 'project', deviceId: 'dev-b', projectPath: PROJECT },
    });
    f.emit({ kind: 'turn-done', sessionId: 's1', turn: 1, durationMs: 1 });
    await settle(hooks);
    const sent = f.fetched as ReturnType<typeof stubFetch>;
    expect(sent.calls).toHaveLength(1);
  });

  it('sends nothing for a disabled hook', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    const { id } = await withHook(hooks);
    await hooks.update(id, { enabled: false });
    f.emit({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    await settle(hooks);
    expect((f.fetched as ReturnType<typeof stubFetch>).calls).toHaveLength(0);
  });

  it('leaves out the opt-in fields without summary', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    await withHook(hooks, { events: ['turn.completed'] });
    f.emit({ kind: 'turn-done', sessionId: 's1', turn: 1, durationMs: 5 });
    await settle(hooks);
    const env = bodyOf((f.fetched as ReturnType<typeof stubFetch>).calls[0] as Call);
    expect(env.data).toEqual({ turn: 1, durationMs: 5 });
  });

  it('sends the same event id to every hook', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    await withHook(hooks);
    await withHook(hooks, { label: 'two' });
    f.emit({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    await settle(hooks);
    const calls = (f.fetched as ReturnType<typeof stubFetch>).calls;
    expect(calls).toHaveLength(2);
    expect(bodyOf(calls[0] as Call).id).toBe(bodyOf(calls[1] as Call).id);
  });
});

describe('signing', () => {
  const sign = (secret: string, t: string, body: string) =>
    createHmac('sha256', secret).update(`${t}.${body}`).digest('hex');

  it('signs the timestamp and the body with the secret returned at creation', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    const { secret } = await withHook(hooks);
    expect(secret).toMatch(/^whsec_[\w-]{43}$/);
    f.emit({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    await settle(hooks);
    const call = (f.fetched as ReturnType<typeof stubFetch>).calls[0] as Call;
    expect(call.headers.get('content-type')).toBe('application/json');
    expect(call.headers.get('user-agent')).toBe('Hoplet-Hook/1');
    const header = call.headers.get('hoplet-signature') ?? '';
    const t = /^t=(\d+),v1=([0-9a-f]{64})$/.exec(header);
    expect(t).not.toBeNull();
    expect(t?.[1]).toBe(String(Math.floor(T0 / 1000)));
    expect(t?.[2]).toBe(sign(secret, t?.[1] ?? '', call.body));
  });

  it('signs with both secrets while a rotation is in its grace period, then with the new one only', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    const { id, secret: first } = await withHook(hooks);
    const rotated = hooks.rotate(id);
    expect(rotated?.secret).not.toBe(first);
    const second = rotated?.secret ?? '';
    f.emit({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    await settle(hooks);
    const calls = (f.fetched as ReturnType<typeof stubFetch>).calls;
    const header = calls[0]?.headers.get('hoplet-signature') ?? '';
    const sigs = [...header.matchAll(/v1=([0-9a-f]{64})/g)].map((m) => m[1]);
    const t = /t=(\d+)/.exec(header)?.[1] ?? '';
    expect(sigs).toEqual([
      sign(second, t, calls[0]?.body ?? ''),
      sign(first, t, calls[0]?.body ?? ''),
    ]);
    f.clock.now += 25 * 3_600_000;
    f.emit({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    await settle(hooks);
    const later = calls[1]?.headers.get('hoplet-signature') ?? '';
    expect(later.match(/v1=/g)).toHaveLength(1);
  });

  it('gives every attempt its own timestamp', async () => {
    const stub = stubFetch([500, 200]);
    const f = setup({ fetch: stub.impl });
    const hooks = f.make(f.bus);
    await withHook(hooks);
    f.emit({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    await settle(hooks);
    f.clock.now += 31_000;
    await settle(hooks);
    expect(stub.calls).toHaveLength(2);
    const ts = stub.calls.map((c) => /t=(\d+)/.exec(c.headers.get('hoplet-signature') ?? '')?.[1]);
    expect(ts[0]).not.toBe(ts[1]);
    expect(stub.calls[0]?.body).toBe(stub.calls[1]?.body);
  });

  it('keeps the secret in a file of mode 0600 and out of the database and the list', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    const { id, secret } = await withHook(hooks);
    const file = path.join(f.dir, 'notify-secrets.json');
    expect(statSync(file).mode & 0o777).toBe(0o600);
    expect(JSON.parse(readFileSync(file, 'utf8')).hooks[id].current).toBe(secret);
    expect(JSON.stringify(hooks.list())).not.toContain(secret);
    expect(JSON.stringify(hooks.get(id))).not.toContain(secret);
  });

  it('reports needs-secret and queues nothing when the secret is gone', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    const { id } = await withHook(hooks);
    hooks.stop();
    rmSync(path.join(f.dir, 'notify-secrets.json'));
    const fresh = new Store(f.dir);
    const again = createHooks(fresh, f.bus, {
      now: () => f.clock.now,
      fetch: (f.fetched as ReturnType<typeof stubFetch>).impl,
      resolve: PUBLIC,
    });
    again.start();
    stops.push(() => again.stop());
    expect(again.get(id)?.status).toBe('needs-secret');
    f.emit({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    await settle(again);
    expect((f.fetched as ReturnType<typeof stubFetch>).calls).toHaveLength(0);
  });
});

describe('retry', () => {
  async function run(statuses: Array<number | 'error'>) {
    const stub = stubFetch(statuses);
    const f = setup({ fetch: stub.impl });
    const hooks = f.make(f.bus);
    const { id } = await withHook(hooks);
    f.emit({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    await settle(hooks);
    const advance = async (ms: number) => {
      f.clock.now += ms;
      await settle(hooks);
    };
    return { ...f, hooks, id, stub, advance };
  }

  it('delivers on the first 2xx', async () => {
    const r = await run([204]);
    expect(r.hooks.deliveries(r.id)).toMatchObject([{ state: 'delivered', attempts: 1 }]);
    expect(r.hooks.get(r.id)?.lastDelivery).toMatchObject({ state: 'delivered' });
    expect(r.hooks.get(r.id)?.pending).toBe(0);
  });

  it.each([
    ['a 5xx', 503],
    ['a 408', 408],
    ['a 429', 429],
    ['a network error', 'error' as const],
  ])(
    'retries %s after 30 seconds, then 2 minutes, 10 minutes and 30 minutes',
    async (_n, first) => {
      const r = await run([first, first, first, first, 200]);
      expect(r.stub.calls).toHaveLength(1);
      expect(r.hooks.get(r.id)?.pending).toBe(1);
      await r.advance(29_000);
      expect(r.stub.calls).toHaveLength(1);
      await r.advance(1_000);
      expect(r.stub.calls).toHaveLength(2);
      await r.advance(119_000);
      expect(r.stub.calls).toHaveLength(2);
      await r.advance(1_000);
      expect(r.stub.calls).toHaveLength(3);
      await r.advance(600_000);
      expect(r.stub.calls).toHaveLength(4);
      await r.advance(1_800_000);
      expect(r.stub.calls).toHaveLength(5);
      expect(r.hooks.deliveries(r.id)).toMatchObject([{ state: 'delivered', attempts: 5 }]);
    },
  );

  it('then retries every 2 hours', async () => {
    const r = await run([500]);
    for (const ms of [30_000, 120_000, 600_000, 1_800_000]) await r.advance(ms);
    expect(r.stub.calls).toHaveLength(5);
    await r.advance(3_600_000);
    expect(r.stub.calls).toHaveLength(5);
    await r.advance(3_600_000);
    expect(r.stub.calls).toHaveLength(6);
  });

  it.each([400, 401, 403, 404, 410, 422])('fails at once on a %i', async (status) => {
    const r = await run([status]);
    expect(r.hooks.deliveries(r.id)).toMatchObject([{ state: 'failed', attempts: 1 }]);
    await r.advance(3_600_000);
    expect(r.stub.calls).toHaveLength(1);
  });

  it('does not disable a hook for answering 410', async () => {
    const r = await run([410]);
    expect(r.hooks.get(r.id)).toMatchObject({ enabled: true, status: 'active' });
  });

  it('gives up 24 hours after the event and marks it failed', async () => {
    const r = await run([500]);
    for (let i = 0; i < 20; i++) await r.advance(2 * 3_600_000);
    const delivery = r.hooks.deliveries(r.id)?.[0];
    expect(delivery?.state).toBe('failed');
    expect(r.hooks.get(r.id)?.pending).toBe(0);
    const calls = r.stub.calls.length;
    await r.advance(3_600_000);
    expect(r.stub.calls).toHaveLength(calls);
  });

  it('drops the oldest waiting delivery past 1000 per hook', async () => {
    const r = await run([500]);
    for (let i = 0; i < 1001; i++) {
      r.store.enqueueHookDelivery(
        {
          hookId: r.id,
          eventId: `evt_x${i}`,
          type: 'session.ended',
          body: '{}',
          expiresAt: T0 + 1e9,
        },
        T0 + 1000 + i,
      );
    }
    expect(r.hooks.get(r.id)?.pending).toBe(1000);
    const dropped = r.store.listHookDeliveries(r.id, 5000).filter((d) => d.state === 'dropped');
    expect(dropped.length).toBeGreaterThanOrEqual(1);
    expect(dropped[0]?.error).toBe('overflow');
  });

  it('drops waiting deliveries when the hook is disabled', async () => {
    const r = await run([500]);
    await r.hooks.update(r.id, { enabled: false });
    await r.advance(31_000);
    expect(r.stub.calls).toHaveLength(1);
    expect(r.hooks.deliveries(r.id)).toMatchObject([{ state: 'dropped', error: 'disabled' }]);
  });

  it('prunes history older than 14 days and beyond 500 per hook', async () => {
    const r = await run([200]);
    expect(r.hooks.deliveries(r.id)).toHaveLength(1);
    r.clock.now += 15 * 86_400_000;
    r.store.pruneHookDeliveries(r.clock.now);
    expect(r.hooks.deliveries(r.id)).toHaveLength(0);
    for (let i = 0; i < 600; i++) {
      const row = r.store.enqueueHookDelivery(
        { hookId: r.id, eventId: `evt_y${i}`, type: 'session.ended', body: '{}', expiresAt: 0 },
        r.clock.now + i,
      );
      r.store.recordHookDelivery(row.id, {
        state: 'delivered',
        attempts: 1,
        settledAt: r.clock.now + i,
      });
    }
    r.store.pruneHookDeliveries(r.clock.now + 1000);
    const rows = (r.store.listHookDeliveries(r.id, 10_000) ?? []).length;
    expect(rows).toBe(500);
  });

  it('sends at most four requests at a time', async () => {
    let active = 0;
    let peak = 0;
    const release: Array<() => void> = [];
    const impl = (async () => {
      active += 1;
      peak = Math.max(peak, active);
      await new Promise<void>((r) => release.push(r));
      active -= 1;
      return new Response(null, { status: 200 });
    }) as typeof fetch;
    const f = setup({ fetch: impl });
    const hooks = f.make(f.bus);
    await withHook(hooks);
    for (let i = 0; i < 10; i++) f.emit({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    await new Promise((r) => setTimeout(r, 20));
    expect(peak).toBe(4);
    while (release.length > 0 || active > 0) {
      release.shift()?.();
      await new Promise((r) => setTimeout(r, 1));
    }
    await hooks.idle();
    expect(peak).toBe(4);
    expect(hooks.get((hooks.list()[0] as { id: string }).id)?.pending).toBe(0);
  });
});

describe('the test route', () => {
  it('sends one hook.test event now, records it and does not retry', async () => {
    const stub = stubFetch([500]);
    const f = setup({ fetch: stub.impl });
    const hooks = f.make(f.bus);
    const { id } = await withHook(hooks);
    const result = await hooks.test(id);
    expect(result?.delivery).toMatchObject({ type: 'hook.test', state: 'failed', attempts: 1 });
    expect(bodyOf(stub.calls[0] as Call)).toMatchObject({
      type: 'hook.test',
      actor: { kind: 'user' },
      data: { hookId: id },
    });
    expect(safeParse(HookEnvelopeSchema, bodyOf(stub.calls[0] as Call)).success).toBe(true);
    f.clock.now += 3_600_000;
    await settle(hooks);
    expect(stub.calls).toHaveLength(1);
  });

  it('works for a disabled hook and answers null for an unknown one', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    const { id } = await withHook(hooks, { enabled: false });
    expect((await hooks.test(id))?.delivery.state).toBe('delivered');
    expect(await hooks.test('nope')).toBeNull();
  });
});

describe('management', () => {
  it('shows a URL without its query and refuses more than 20 hooks', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    const { id } = await withHook(hooks);
    expect(hooks.get(id)?.url).toBe('https://hooks.example/a');
    for (let i = 1; i < 20; i++) await withHook(hooks, { label: `h${i}` });
    const extra = await hooks.create({ url: URL_A, label: 'x', events: ['session.ended'] });
    expect(extra).toMatchObject({ ok: false, error: 'too-many-hooks' });
  });

  it('refuses a private address and plain http without the allow switch', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    expect(
      await hooks.create({ url: 'http://hooks.example/a', label: 'x', events: ['session.ended'] }),
    ).toMatchObject({ ok: false, error: 'bad-url', reason: 'http-needs-allow-private' });
    expect(
      await hooks.create({ url: 'https://127.0.0.1/a', label: 'x', events: ['session.ended'] }),
    ).toMatchObject({ ok: false, error: 'bad-url', reason: 'blocked-address' });
  });

  it('removes a hook with its deliveries and secret', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    const { id } = await withHook(hooks);
    f.emit({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    await settle(hooks);
    expect(hooks.remove(id)).toBe(true);
    expect(hooks.get(id)).toBeNull();
    expect(hooks.deliveries(id)).toBeNull();
    expect(f.store.hookSecret(id)).toBeNull();
    expect(hooks.remove(id)).toBe(false);
  });

  it('audits changes without the URL, the secret or the payload', async () => {
    const f = setup();
    const hooks = f.make(f.bus);
    const { id, secret } = await withHook(hooks);
    await hooks.update(id, { label: 'Renamed', url: 'https://hooks.example/b?key=zzz' });
    hooks.rotate(id);
    await hooks.test(id);
    hooks.remove(id);
    const events = f.store.listAudit({}, { order: 'asc', limit: 500 }).events;
    const mine = events.filter(
      (e) => (e as { summary?: { area?: string } }).summary?.area === 'hooks',
    );
    expect(
      mine.map((e) => (e as unknown as { summary: { action: string } }).summary.action),
    ).toEqual(['created', 'edited', 'rotated', 'tested', 'deleted']);
    const text = JSON.stringify(events);
    expect(text).not.toContain(secret);
    expect(text).not.toContain('hooks.example');
    expect(text).not.toContain('zzz');
  });
});

describe('the address check', () => {
  it.each([
    ['93.184.216.34', 'public'],
    ['10.0.0.5', 'private'],
    ['172.16.3.4', 'private'],
    ['192.168.1.1', 'private'],
    ['127.0.0.1', 'private'],
    ['100.64.0.1', 'private'],
    ['169.254.169.254', 'never'],
    ['0.0.0.0', 'never'],
    ['224.0.0.1', 'never'],
    ['255.255.255.255', 'never'],
    ['2606:4700:4700::1111', 'public'],
    ['::1', 'private'],
    ['fe80::1', 'never'],
    ['fd00::1', 'private'],
    ['::ffff:169.254.169.254', 'never'],
    ['::ffff:10.0.0.1', 'private'],
    ['::ffff:93.184.216.34', 'public'],
    ['64:ff9b::a9fe:a9fe', 'never'],
  ])('classifies %s as %s', (address, expected) => {
    expect(classifyAddress(address)).toBe(expected);
  });

  it('refuses a URL by its scheme, credentials and resolved addresses', async () => {
    expect(await refuseUrl('nonsense', false, PUBLIC)).toBe('invalid-url');
    expect(await refuseUrl('ftp://h.example/', false, PUBLIC)).toBe('scheme');
    expect(await refuseUrl('https://u:p@h.example/', false, PUBLIC)).toBe('credentials');
    expect(await refuseUrl('http://h.example/', false, PUBLIC)).toBe('http-needs-allow-private');
    expect(await refuseUrl('http://h.example/', true, PUBLIC)).toBe('http-needs-allow-private');
    expect(await refuseUrl('https://h.example/', false, PUBLIC)).toBeNull();
    expect(await refuseUrl('https://h.example/', false, async () => [])).toBe('unresolvable');
    expect(
      await refuseUrl('https://h.example/', false, async () => {
        throw new Error('ENOTFOUND');
      }),
    ).toBe('unresolvable');
    expect(await refuseUrl('https://h.example/', true, async () => ['169.254.169.254'])).toBe(
      'blocked-address',
    );
    // One bad address among good ones is enough to refuse.
    expect(
      await refuseUrl('https://h.example/', false, async () => ['93.184.216.34', '10.0.0.1']),
    ).toBe('blocked-address');
    expect(await refuseUrl('http://192.168.1.5/', true, PUBLIC)).toBeNull();
  });

  async function local(status = 200, location?: string) {
    const seen: string[] = [];
    const server = createServer((req, res) => {
      seen.push(`${req.method} ${req.url} ${req.headers.host}`);
      req.resume();
      req.on('end', () => {
        res.writeHead(status, location ? { location } : {});
        res.end('secret body');
      });
    });
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    return { seen, port: (server.address() as AddressInfo).port };
  }

  it('connects to loopback only with the allow switch, over http', async () => {
    const s = await local();
    const url = `http://127.0.0.1:${s.port}/h`;
    const blocked = createPinnedFetch({ allowPrivate: false, resolve: PUBLIC });
    await expect(blocked(url, { method: 'POST', body: '{}' })).rejects.toThrow();
    expect(s.seen).toHaveLength(0);
    const open = createPinnedFetch({ allowPrivate: true, resolve: PUBLIC });
    const res = await open(url, { method: 'POST', body: '{}' });
    expect(res.status).toBe(200);
    expect(await res.text()).toBe('');
    expect(s.seen).toEqual([`POST /h 127.0.0.1:${s.port}`]);
  });

  it('connects to the address it checked, not to what the name resolves to later', async () => {
    const s = await local();
    let calls = 0;
    // The first lookup is the check; a rebinding resolver would answer something else the second time.
    const resolve = async () => {
      calls += 1;
      return calls === 1 ? ['127.0.0.1'] : ['93.184.216.34'];
    };
    const open = createPinnedFetch({ allowPrivate: true, resolve });
    const res = await open(`http://rebind.example:${s.port}/h`, { method: 'POST', body: '{}' });
    expect(res.status).toBe(200);
    expect(s.seen).toHaveLength(1);
  });

  it('follows no redirect', async () => {
    const s = await local(302, 'http://169.254.169.254/latest');
    const open = createPinnedFetch({ allowPrivate: true, resolve: PUBLIC });
    const res = await open(`http://127.0.0.1:${s.port}/h`, { method: 'POST', body: '{}' });
    expect(res.status).toBe(302);
    expect(s.seen).toHaveLength(1);
  });

  it('fails a delivery whose address turned bad since it was saved, without retrying', async () => {
    const s = await local();
    let bad = false;
    const resolve = async () => (bad ? ['169.254.169.254'] : ['127.0.0.1']);
    const f = setup({
      fetch: createPinnedFetch({ allowPrivate: true, resolve }),
      allowPrivate: true,
    });
    const hooks = createHooks(f.store, f.bus, {
      now: () => f.clock.now,
      fetch: createPinnedFetch({ allowPrivate: true, resolve }),
      resolve,
      allowPrivate: true,
    });
    hooks.start();
    stops.push(() => hooks.stop());
    const { id } = await withHook(hooks, { url: `http://h.example:${s.port}/h` });
    bad = true;
    f.emit({ kind: 'ended', sessionId: 's1', reason: 'stopped' });
    await settle(hooks);
    expect(s.seen).toHaveLength(0);
    expect(hooks.deliveries(id)).toMatchObject([{ state: 'failed', attempts: 1 }]);
  });
});

describe('routes', () => {
  async function api() {
    const f = setup();
    const hooks = f.make(f.bus);
    const server = createServer();
    createApi(f.store, { port: 0, dataDir: f.dir, token: 'host-token', webDir: null }, f.control, {
      hooks,
      now: () => f.clock.now,
    }).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const call = async (
      method: string,
      route: string,
      body?: unknown,
      authorization: string | null = 'Bearer host-token',
    ) => {
      const res = await fetch(`${base}${route}`, {
        method,
        headers: {
          ...(authorization ? { authorization } : {}),
          ...(body === undefined ? {} : { 'content-type': 'application/json' }),
        },
        body: body === undefined ? null : JSON.stringify(body),
      });
      const text = await res.text();
      return {
        status: res.status,
        body: text ? (JSON.parse(text) as Json) : {},
      };
    };
    return { ...f, hooks, call };
  }

  const create = { url: URL_A, label: 'Chat', events: ['session.ended'] };

  it('needs the host token on every hook route', async () => {
    const a = await api();
    const { id } = await withHook(a.hooks);
    const routes: Array<[string, string, unknown?]> = [
      ['GET', '/api/hooks'],
      ['POST', '/api/hooks', create],
      ['GET', `/api/hooks/${id}`],
      ['PATCH', `/api/hooks/${id}`, { label: 'x' }],
      ['DELETE', `/api/hooks/${id}`],
      ['POST', `/api/hooks/${id}/rotate-secret`],
      ['GET', `/api/hooks/${id}/deliveries`],
      ['POST', `/api/hooks/${id}/test`],
    ];
    for (const [method, route, body] of routes) {
      expect((await a.call(method, route, body, null)).status, `${method} ${route}`).toBe(401);
      expect((await a.call(method, route, body, 'Bearer wrong')).status, route).toBe(401);
    }
    // Nothing happened behind the refusals.
    expect(a.hooks.list()).toHaveLength(1);
    expect(a.hooks.get(id)?.label).toBe('Chat');
  });

  it('refuses a ticket-style session token on hook routes', async () => {
    const a = await api();
    const minted = await a.call('POST', '/api/ws-tickets');
    expect(minted.status).toBe(200);
    const ticket = String(minted.body.ticket);
    expect((await a.call('GET', '/api/hooks', undefined, `Bearer ${ticket}`)).status).toBe(401);
  });

  it('creates, lists, updates, rotates, tests and deletes', async () => {
    const stub = stubFetch([200]);
    const f = setup({ fetch: stub.impl });
    const hooks = f.make(f.bus);
    const server = createServer();
    createApi(f.store, { port: 0, dataDir: f.dir, token: 'host-token', webDir: null }, f.control, {
      hooks,
    }).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const call = async (method: string, route: string, body?: unknown) => {
      const res = await fetch(`${base}${route}`, {
        method,
        headers: {
          authorization: 'Bearer host-token',
          ...(body === undefined ? {} : { 'content-type': 'application/json' }),
        },
        body: body === undefined ? null : JSON.stringify(body),
      });
      const text = await res.text();
      return { status: res.status, body: text ? (JSON.parse(text) as Json) : {} };
    };

    const created = await call('POST', '/api/hooks', create);
    expect(created.status).toBe(201);
    expect(created.body.secret).toMatch(/^whsec_/);
    const id = created.body.hook.id as string;
    expect(created.body.hook).toMatchObject({
      kind: 'webhook',
      url: 'https://hooks.example/a',
      status: 'active',
      enabled: true,
    });

    const listed = await call('GET', '/api/hooks');
    expect(listed.status).toBe(200);
    expect(JSON.stringify(listed.body)).not.toContain(created.body.secret);
    expect(listed.body.hooks).toHaveLength(1);

    const patched = await call('PATCH', `/api/hooks/${id}`, { label: 'Renamed', summary: true });
    expect(patched.status).toBe(200);
    expect(patched.body).toMatchObject({ label: 'Renamed', summary: true });

    const rotated = await call('POST', `/api/hooks/${id}/rotate-secret`);
    expect(rotated.status).toBe(200);
    expect(rotated.body.secret).not.toBe(created.body.secret);

    const tested = await call('POST', `/api/hooks/${id}/test`);
    expect(tested.status).toBe(200);
    expect(tested.body.delivery).toMatchObject({ type: 'hook.test', state: 'delivered' });

    const history = await call('GET', `/api/hooks/${id}/deliveries`);
    expect(history.body.deliveries).toHaveLength(1);

    const removed = await call('DELETE', `/api/hooks/${id}`);
    expect(removed.status).toBe(200);
    expect(removed.body.hooks).toEqual([]);
    expect((await call('GET', `/api/hooks/${id}`)).body.code).toBe('not-found');
    expect((await call('POST', `/api/hooks/${id}/test`)).status).toBe(404);
  });

  it('refuses a bad body, a bad URL and the 21st hook', async () => {
    const a = await api();
    expect((await a.call('POST', '/api/hooks', { label: 'x' })).status).toBe(400);
    expect((await a.call('POST', '/api/hooks', { ...create, events: [] })).status).toBe(400);
    expect((await a.call('POST', '/api/hooks', { ...create, extra: 1 })).status).toBe(400);
    const http = await a.call('POST', '/api/hooks', { ...create, url: 'http://hooks.example/a' });
    expect(http.status).toBe(400);
    expect(http.body).toMatchObject({
      code: 'invalid-request',
      reason: 'http-needs-allow-private',
    });
    for (let i = 0; i < 20; i++)
      expect((await a.call('POST', '/api/hooks', create)).status).toBe(201);
    expect((await a.call('POST', '/api/hooks', create)).body.code).toBe('too-large');
  });
});
