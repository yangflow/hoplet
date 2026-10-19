import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  type AgentRuntimeState,
  MAX_QUEUE_ITEMS,
  PROTOCOL_VERSION,
  type RuntimeCapabilities,
  type ServerMessage,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { createApi } from '../src/api/index.ts';
import { createControl, type DeviceLink } from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
const servers: Server[] = [];

afterEach(async () => {
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

const line = JSON.stringify({
  type: 'user',
  cwd: '/tmp/p',
  message: { role: 'user', content: 'x' },
});
const meta = {
  sessionId: 's1',
  provider: 'claude' as const,
  projectDir: 'p',
  filePath: '/x',
  deviceId: 'dev-a',
};
const caps = (steer = true): RuntimeCapabilities => ({
  permissions: true,
  interrupt: true,
  steer,
  config: [],
});
const tick = (ms = 15) => new Promise((r) => setTimeout(r, ms));

function newStore(): { store: Store; dir: string } {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-queue-'));
  dirs.push(dir);
  const store = new Store(dir);
  store.append(meta, [line]);
  return { store, dir };
}

type Sent = { type: string; [k: string]: unknown };

function setup(options: { promptTimeoutMs?: number } = {}) {
  const { store, dir } = newStore();
  const control = createControl(store, { promptTimeoutMs: 300, ...options });
  const sent: Sent[] = [];
  const events: ServerMessage[] = [];
  control.subscribe((m) => events.push(m));
  const link: DeviceLink = {
    send: (m) => void sent.push(m as unknown as Sent),
    sendAndWait: async (m) => void sent.push(m as unknown as Sent),
    isOpen: () => true,
  };
  control.attach('dev-a', link);
  const state = (s: AgentRuntimeState, extra: { error?: string; steer?: boolean } = {}) =>
    control.receive(link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.state',
      sessionId: 's1',
      state: s,
      ...(extra.error ? { error: extra.error } : {}),
      capabilities: caps(extra.steer ?? true),
    });
  const prompts = () => sent.filter((m) => m.type === 'runtime.prompt');
  const add = (id: string, text = id) => control.queueAdd('s1', { id, text, attachments: [] });
  return { store, dir, control, link, sent, events, state, prompts, add };
}

describe('the store queue', () => {
  it('appends, ignores a repeated id and stops at the limit', () => {
    const { store } = newStore();
    expect(store.queueAdd('s1', { id: 'a', text: 'one', attachments: [] }).ok).toBe(true);
    expect(store.queueAdd('s1', { id: 'a', text: 'other', attachments: [] }).ok).toBe(true);
    expect(store.queueOf('s1').items).toEqual([
      { id: 'a', text: 'one', attachments: [], version: 1, state: 'queued' },
    ]);
    for (let i = 1; i < MAX_QUEUE_ITEMS; i++) {
      store.queueAdd('s1', { id: `m${i}`, text: 'x', attachments: [] });
    }
    expect(store.queueAdd('s1', { id: 'over', text: 'x', attachments: [] })).toEqual({
      ok: false,
      reason: 'full',
    });
  });

  it('edits and removes by version, and refuses a stale or sending item', () => {
    const { store } = newStore();
    store.queueAdd('s1', { id: 'a', text: 'one', attachments: [] });
    expect(store.queueEdit('s1', 'a', 2, { text: 'x', attachments: [] }).ok).toBe(false);
    const edited = store.queueEdit('s1', 'a', 1, { text: 'two', attachments: [] });
    expect(edited.ok && edited.item?.version).toBe(2);
    expect(store.queueRemove('s1', 'a', 1).ok).toBe(false);
    expect(store.queueClaim('s1', 'a', 2).ok).toBe(true);
    expect(store.queueEdit('s1', 'a', 2, { text: 'x', attachments: [] }).ok).toBe(false);
    expect(store.queueRemove('s1', 'a', 2).ok).toBe(false);
    store.queueRestore('s1', 'a');
    expect(store.queueRemove('s1', 'a', 2).ok).toBe(true);
    expect(store.queueOf('s1').items).toEqual([]);
  });

  it('reorders exactly the waiting messages', () => {
    const { store } = newStore();
    for (const id of ['a', 'b', 'c']) store.queueAdd('s1', { id, text: id, attachments: [] });
    expect(store.queueReorder('s1', ['c', 'a', 'b']).ok).toBe(true);
    expect(store.queueOf('s1').items.map((i) => i.id)).toEqual(['c', 'a', 'b']);
    expect(store.queueReorder('s1', ['c', 'a']).ok).toBe(false);
    expect(store.queueReorder('s1', ['c', 'a', 'a']).ok).toBe(false);
    expect(store.queueReorder('s1', ['c', 'a', 'zzz']).ok).toBe(false);
    // The message on its way keeps its place while the others move.
    const head = store.queueNext('s1');
    store.queueClaim('s1', head?.id ?? '', head?.version ?? 0);
    expect(store.queueReorder('s1', ['b', 'a']).ok).toBe(true);
    expect(store.queueOf('s1').items.map((i) => i.id)).toEqual(['c', 'b', 'a']);
    expect(store.queueNext('s1')).toBeNull();
  });

  it('forgets a pause once the queue is empty, and clearing keeps a message on its way', () => {
    const { store } = newStore();
    store.queueSetPause('s1', 'error', 'x');
    expect(store.queueOf('s1').paused).toBeUndefined();
    store.queueAdd('s1', { id: 'a', text: 'a', attachments: [] });
    store.queueAdd('s1', { id: 'b', text: 'b', attachments: [] });
    store.queueSetPause('s1', 'error', 'boom');
    expect(store.queueOf('s1')).toMatchObject({ paused: 'error', error: 'boom' });
    store.queueClaim('s1', 'a', 1);
    store.queueClear('s1');
    expect(store.queueOf('s1')).toEqual({
      items: [{ id: 'a', text: 'a', attachments: [], version: 1, state: 'sending' }],
    });
    store.queueSettle('s1', 'a');
    expect(store.queueOf('s1').items).toEqual([]);
  });

  it('survives a restart: nothing is sending and a queue with messages waits for a resume', () => {
    const { store, dir } = newStore();
    store.queueAdd('s1', { id: 'a', text: 'a', attachments: [] });
    store.queueClaim('s1', 'a', 1);
    const reopened = new Store(dir);
    expect(reopened.queueOf('s1')).toEqual({
      items: [{ id: 'a', text: 'a', attachments: [], version: 1, state: 'queued' }],
      paused: 'interrupted',
    });
  });
});

describe('the queue drain', () => {
  it('sends a message added to an idle session at once and settles it when the agent takes it', async () => {
    const t = setup();
    t.state('idle');
    t.add('a', 'first');
    await tick();
    expect(t.prompts()).toHaveLength(1);
    expect(t.prompts()[0]).toMatchObject({ sessionId: 's1', text: 'first' });
    expect(t.store.queueOf('s1').items[0]?.state).toBe('sending');
    t.state('running');
    await tick();
    expect(t.store.queueOf('s1').items).toEqual([]);
    expect(t.events.filter((e) => e.type === 'session.queue').length).toBeGreaterThanOrEqual(2);
  });

  it('holds messages during a turn and sends one per finished turn, in order', async () => {
    const t = setup();
    t.state('running');
    t.add('a', 'one');
    t.add('b', 'two');
    await tick();
    expect(t.prompts()).toHaveLength(0);
    t.state('idle');
    await tick();
    expect(t.prompts().map((p) => p.text)).toEqual(['one']);
    t.state('running');
    await tick();
    expect(t.prompts()).toHaveLength(1);
    t.state('idle');
    await tick();
    expect(t.prompts().map((p) => p.text)).toEqual(['one', 'two']);
  });

  it('waits out a pending permission and carries on after the turn', async () => {
    const t = setup();
    t.state('waiting-permission');
    t.add('a');
    await tick();
    expect(t.prompts()).toHaveLength(0);
    expect(t.store.queueOf('s1').paused).toBeUndefined();
    t.state('idle');
    await tick();
    expect(t.prompts()).toHaveLength(1);
  });

  it('pauses on a failed turn with its error and resumes on request', async () => {
    const t = setup();
    t.state('running');
    t.add('a');
    t.state('failed', { error: 'exit 1' });
    await tick();
    expect(t.control.queueOf('s1')).toMatchObject({ paused: 'error', error: 'exit 1' });
    t.state('idle');
    await tick();
    expect(t.prompts()).toHaveLength(0);
    t.control.queueResume('s1');
    await tick();
    expect(t.prompts()).toHaveLength(1);
  });

  it('pauses on an interrupt and waits for an explicit resume', async () => {
    const t = setup();
    t.state('running');
    t.add('a');
    expect(t.control.interrupt('s1', 'dev-a').ok).toBe(true);
    expect(t.control.queueOf('s1').paused).toBe('interrupted');
    t.state('idle');
    await tick();
    expect(t.prompts()).toHaveLength(0);
    t.control.queueResume('s1');
    await tick();
    expect(t.prompts()).toHaveLength(1);
  });

  it('puts a refused message back and pauses with the reason', async () => {
    const t = setup();
    t.state('idle');
    t.add('a');
    await tick();
    const prompt = t.prompts()[0] as Sent;
    t.control.receive(t.link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.rejected',
      sessionId: 's1',
      promptId: prompt.promptId as string,
      reason: 'claude is not installed',
    });
    await tick();
    expect(t.control.queueOf('s1')).toMatchObject({
      paused: 'error',
      error: 'claude is not installed',
      items: [{ id: 'a', state: 'queued' }],
    });
  });

  it('puts a message back without a pause when the session turned busy meanwhile', async () => {
    const t = setup();
    t.state('idle');
    t.add('a');
    await tick();
    const prompt = t.prompts()[0] as Sent;
    t.control.receive(t.link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.rejected',
      sessionId: 's1',
      promptId: prompt.promptId as string,
      reason: 'busy',
    });
    await tick();
    expect(t.control.queueOf('s1')).toEqual({
      items: [{ id: 'a', text: 'a', attachments: [], version: 1, state: 'queued' }],
    });
  });

  it('keeps messages of an offline owner and does not send to it', async () => {
    const t = setup();
    t.control.detach(t.link);
    t.add('a');
    await tick();
    expect(t.prompts()).toHaveLength(0);
    expect(t.control.queueOf('s1').items).toHaveLength(1);
  });

  it('never sends to a session an external process runs', async () => {
    const t = setup();
    t.state('external');
    t.add('a');
    await tick();
    expect(t.prompts()).toHaveLength(0);
  });
});

describe('steer', () => {
  it('forwards a message to a running turn and resolves when the agent wrote it', async () => {
    const t = setup();
    t.state('running');
    const result = t.control.steer('s1', 'dev-a', 'st1', 'go left');
    const msg = t.sent.find((m) => m.type === 'runtime.steer');
    expect(msg).toMatchObject({ steerId: 'st1', text: 'go left' });
    t.control.receive(t.link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.steered',
      sessionId: 's1',
      steerId: 'st1',
    });
    expect(await result).toEqual({ ok: true });
    // A repeated steerId is the same steer.
    expect(await t.control.steer('s1', 'dev-a', 'st1', 'go left')).toEqual({ ok: true });
    expect(t.sent.filter((m) => m.type === 'runtime.steer')).toHaveLength(1);
  });

  it('refuses outside a running turn, during a permission and without the capability', async () => {
    const t = setup();
    t.state('idle');
    expect(await t.control.steer('s1', 'dev-a', 'a', 'x')).toEqual({
      ok: false,
      error: 'not-running',
    });
    t.state('waiting-permission');
    expect(await t.control.steer('s1', 'dev-a', 'b', 'x')).toEqual({
      ok: false,
      error: 'steer-unavailable',
    });
    t.state('running', { steer: false });
    expect(await t.control.steer('s1', 'dev-a', 'c', 'x')).toEqual({
      ok: false,
      error: 'steer-unavailable',
    });
    expect(t.sent.filter((m) => m.type === 'runtime.steer')).toHaveLength(0);
  });

  it('maps the agent refusal and times out without an answer', async () => {
    const t = setup();
    t.state('running');
    const refused = t.control.steer('s1', 'dev-a', 'a', 'x');
    t.control.receive(t.link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.rejected',
      sessionId: 's1',
      promptId: 'a',
      reason: 'permission',
    });
    expect(await refused).toEqual({ ok: false, error: 'steer-unavailable' });
    expect(await t.control.steer('s1', 'dev-a', 'b', 'x')).toEqual({
      ok: false,
      error: 'timeout',
    });
  });

  it('converts a queued message: it leaves the queue when taken and returns when refused', async () => {
    const t = setup();
    t.state('running');
    t.add('a', 'one');
    t.add('b', 'two');
    expect((await t.control.queueSteer('s1', 'dev-a', 'a', 7)).ok).toBe(false);
    const done = t.control.queueSteer('s1', 'dev-a', 'b', 1);
    const msg = t.sent.find((m) => m.type === 'runtime.steer') as Sent;
    expect(msg.text).toBe('two');
    expect(t.control.queueOf('s1').items.map((i) => i.state)).toEqual(['queued', 'sending']);
    t.control.receive(t.link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.steered',
      sessionId: 's1',
      steerId: msg.steerId as string,
    });
    const outcome = await done;
    expect(outcome.ok && outcome.queue.items.map((i) => i.id)).toEqual(['a']);

    const refused = t.control.queueSteer('s1', 'dev-a', 'a', 1);
    const second = t.sent.filter((m) => m.type === 'runtime.steer')[1] as Sent;
    t.control.receive(t.link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.rejected',
      sessionId: 's1',
      promptId: second.steerId as string,
      reason: 'not-running',
    });
    const back = await refused;
    expect(back.ok && back.steer).toEqual({ ok: false, error: 'not-running' });
    expect(t.control.queueOf('s1').items).toEqual([
      { id: 'a', text: 'one', attachments: [], version: 1, state: 'queued' },
    ]);
  });
});

describe('the queue routes', () => {
  async function boot() {
    const t = setup();
    const config = { port: 0, dataDir: t.dir, token: 'tok', webDir: null };
    const server = createServer();
    createApi(t.store, config, t.control).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const call = async (method: string, url: string, body?: unknown) => {
      const res = await fetch(base + url, {
        method,
        headers: { authorization: 'Bearer tok', 'content-type': 'application/json' },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      // biome-ignore lint/suspicious/noExplicitAny: a test reads fields of the JSON answer freely
      return { status: res.status, body: (await res.json()) as Record<string, any> };
    };
    return { ...t, call };
  }

  it('adds, edits, reorders, deletes with a version and reports conflicts', async () => {
    const t = await boot();
    t.state('running');
    let r = await t.call('POST', '/api/sessions/s1/queue', { id: 'a', text: 'one' });
    expect(r.status).toBe(201);
    r = await t.call('POST', '/api/sessions/s1/queue', { id: 'b', text: 'two' });
    r = await t.call('POST', '/api/sessions/s1/queue/order', { ids: ['b', 'a'] });
    expect(r.body.items.map((i: { id: string }) => i.id)).toEqual(['b', 'a']);
    r = await t.call('PATCH', '/api/sessions/s1/queue/a', { version: 1, text: 'ONE' });
    expect(r.status).toBe(200);
    expect(r.body.items.find((i: { id: string }) => i.id === 'a').version).toBe(2);
    r = await t.call('PATCH', '/api/sessions/s1/queue/a', { version: 1, text: 'late' });
    expect(r).toMatchObject({ status: 409, body: { code: 'queue-conflict' } });
    r = await t.call('DELETE', '/api/sessions/s1/queue/a?version=1');
    expect(r.status).toBe(409);
    r = await t.call('DELETE', '/api/sessions/s1/queue/a?version=2');
    expect(r.body.items.map((i: { id: string }) => i.id)).toEqual(['b']);
    r = await t.call('GET', '/api/sessions/s1/queue');
    expect(r.body.items).toHaveLength(1);
    r = await t.call('POST', '/api/sessions/s1/queue/clear');
    expect(r.body.items).toEqual([]);
  });

  it('applies the limits of a prompt and of the queue', async () => {
    const t = await boot();
    t.state('running');
    const big = await t.call('POST', '/api/sessions/s1/queue', { id: 'a', text: 'x'.repeat(8001) });
    expect(big.body.code).toBe('too-large');
    for (let i = 0; i < MAX_QUEUE_ITEMS; i++) {
      await t.call('POST', '/api/sessions/s1/queue', { id: `m${i}`, text: 'x' });
    }
    const over = await t.call('POST', '/api/sessions/s1/queue', { id: 'over', text: 'x' });
    expect(over).toMatchObject({ status: 409, body: { code: 'queue-full' } });
    const missing = await t.call('GET', '/api/sessions/nope/queue');
    expect(missing.status).toBe(404);
  });

  it('steers a running session and says when it cannot', async () => {
    const t = await boot();
    t.state('waiting-permission');
    const blocked = await t.call('POST', '/api/sessions/s1/steer', { steerId: 'x1', text: 'hi' });
    expect(blocked).toMatchObject({ status: 409, body: { code: 'steer-unavailable' } });
    t.state('idle');
    const idle = await t.call('POST', '/api/sessions/s1/steer', { steerId: 'x2', text: 'hi' });
    expect(idle.body.code).toBe('not-running');
    t.state('running');
    const pending = t.call('POST', '/api/sessions/s1/steer', { steerId: 'x3', text: 'hi' });
    await tick();
    t.control.receive(t.link, {
      v: PROTOCOL_VERSION,
      type: 'runtime.steered',
      sessionId: 's1',
      steerId: 'x3',
    });
    expect(await pending).toMatchObject({ status: 202, body: { ok: true, steerId: 'x3' } });
  });
});
