import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  type ClientMessage,
  type HookAnnounce,
  type HookEnvelope,
  HookEnvelopeSchema,
  PROTOCOL_VERSION,
  type ServerMessage,
  safeParse,
} from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import {
  type Control,
  type ControlEvent,
  type ControlReply,
  createControl,
  type DeviceLink,
} from '../src/control/index.ts';
import { createHooks, type Hooks } from '../src/hooks/index.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
const stops: Array<() => void> = [];

afterEach(() => {
  for (const stop of stops.splice(0)) stop();
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

const T0 = Date.UTC(2026, 9, 16, 12, 0);
const PROJECT = '/work/app';
const ID = 'script:dev-a:notify';

function announce(over: Partial<HookAnnounce> = {}): HookAnnounce {
  return {
    id: 'notify',
    command: '/opt/notify.sh',
    events: ['session.ended'],
    projects: [],
    timeoutMs: 10_000,
    env: ['MY_TOKEN'],
    runs: 0,
    failed: 0,
    dropped: 0,
    ...over,
  };
}

function fakeLink(open = { value: true }): DeviceLink & { sent: ServerMessage[] } {
  const link = {
    sent: [] as ServerMessage[],
    send(msg: ServerMessage) {
      link.sent.push(msg);
    },
    async sendAndWait(msg: ServerMessage) {
      link.sent.push(msg);
    },
    isOpen: () => open.value,
  };
  return link;
}

function setup(hooksOfDevice: HookAnnounce[] = [announce()]) {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-script-hooks-'));
  dirs.push(dir);
  const store = new Store(dir);
  store.upsertDevice('dev-a', 'Mac', '0.1.0');
  store.upsertDevice('dev-b', 'Linux', '0.1.0');
  store.append(
    { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'dev-a' },
    [JSON.stringify({ type: 'user', cwd: PROJECT, message: { role: 'user', content: 'hi' } })],
  );
  const clock = { now: T0 };
  const control = createControl(store, { now: () => clock.now, startTimeoutMs: 100 });
  const link = fakeLink();
  control.attach('dev-a', link);
  store.replaceScriptHooks('dev-a', hooksOfDevice, T0);
  const listeners: Array<(e: ControlEvent) => void> = [];
  const bus = {
    onEvent(l: (e: ControlEvent) => void) {
      listeners.push(l);
      return () => listeners.splice(listeners.indexOf(l), 1);
    },
    sendHookEvent: (...a: Parameters<Control['sendHookEvent']>) => control.sendHookEvent(...a),
    onHookResult: (l: Parameters<Control['onHookResult']>[0]) => control.onHookResult(l),
    onDevicesGone: (l: Parameters<Control['onDevicesGone']>[0]) => control.onDevicesGone(l),
    ownerOnline: (id: string) => control.ownerOnline(id),
  } as unknown as Control;
  const emit = (e: ControlEvent) => {
    for (const l of [...listeners]) l(e);
  };
  const hooks: Hooks = createHooks(store, bus, { now: () => clock.now });
  hooks.start();
  stops.push(() => hooks.stop());
  const result = (over: Record<string, unknown>) =>
    control.receive(link, {
      v: PROTOCOL_VERSION,
      type: 'hook.result',
      hookId: 'notify',
      ok: true,
      durationMs: 12,
      stderr: '',
      ...over,
    } as ClientMessage as ControlReply);
  return { store, control, link, clock, emit, hooks, result };
}

const hookEvents = (link: { sent: ServerMessage[] }) =>
  link.sent.filter((m) => m.type === 'hook.event') as Array<{
    hookId: string;
    event: HookEnvelope;
  }>;

const ended: ControlEvent = { kind: 'ended', sessionId: 's1', reason: 'stopped' };

describe('script hooks', () => {
  it('lists the announced hook, read only, with its machine', () => {
    const f = setup();
    const [hook] = f.hooks.list();
    expect(hook).toMatchObject({
      id: ID,
      kind: 'script',
      label: 'notify',
      machine: { id: 'dev-a', name: 'Mac', online: true },
      command: '/opt/notify.sh',
      env: ['MY_TOKEN'],
      enabled: true,
      status: 'active',
      pending: 0,
    });
    expect(f.hooks.get(ID)?.kind).toBe('script');
    expect(f.hooks.isScript(ID)).toBe(true);
    expect(f.hooks.remove(ID)).toBe(false);
    expect(f.hooks.rotate(ID)).toBeNull();
  });

  it('sends a matching event to the machine of the session, with the project path', () => {
    const f = setup();
    f.emit(ended);
    const [sent] = hookEvents(f.link);
    expect(sent?.hookId).toBe('notify');
    expect(sent?.event.type).toBe('session.ended');
    expect(sent?.event.session.project).toEqual({ name: 'app', path: PROJECT });
    expect(safeParse(HookEnvelopeSchema, sent?.event).success).toBe(true);
    expect(f.hooks.list()[0]).toMatchObject({ pending: 1 });
  });

  it('settles the delivery with the result and keeps the run', () => {
    const f = setup();
    f.emit(ended);
    const eventId = hookEvents(f.link)[0]?.event.id;
    f.result({ eventId, ok: false, exitCode: 3, durationMs: 40, stderr: 'boom' });
    const [d] = f.hooks.deliveries(ID) ?? [];
    expect(d).toMatchObject({
      state: 'failed',
      error: 'exit-3',
      run: { exitCode: 3, durationMs: 40, stderr: 'boom' },
    });
    expect(f.hooks.list()[0]).toMatchObject({
      pending: 0,
      lastDelivery: { state: 'failed', error: 'exit-3' },
    });
  });

  it('maps ok, timeout and dropped results', () => {
    const f = setup([announce({ events: ['*'] })]);
    const states: Array<[Record<string, unknown>, string, string | undefined]> = [
      [{ ok: true }, 'delivered', undefined],
      [{ ok: false, error: 'timeout' }, 'failed', 'timeout'],
      [{ ok: false, error: 'dropped' }, 'dropped', 'dropped'],
    ];
    for (const [over, state, error] of states) {
      f.emit(ended);
      const sent = hookEvents(f.link);
      f.result({ eventId: sent[sent.length - 1]?.event.id, ...over });
      const [latest] = f.hooks.deliveries(ID) ?? [];
      expect(latest?.state).toBe(state);
      expect(latest?.error).toBe(error);
    }
  });

  it('sends nothing for another event type, another project or a disabled hook', () => {
    const f = setup([announce({ projects: ['/other'] })]);
    f.emit(ended);
    f.emit({ kind: 'turn-done', sessionId: 's1', turn: 1, durationMs: 1 });
    expect(hookEvents(f.link)).toHaveLength(0);
    const g = setup();
    g.hooks.update(ID, { enabled: false });
    g.emit(ended);
    expect(hookEvents(g.link)).toHaveLength(0);
    expect(g.hooks.list()[0]).toMatchObject({ enabled: false, status: 'disabled' });
  });

  it('matches a project under the listed directory', () => {
    const f = setup([announce({ projects: ['/work'] })]);
    f.emit(ended);
    expect(hookEvents(f.link)).toHaveLength(1);
  });

  it('sends only to the machine that owns the session', () => {
    const f = setup();
    f.store.replaceScriptHooks('dev-b', [announce()], T0);
    f.emit(ended);
    expect(hookEvents(f.link)).toHaveLength(1);
    expect(f.hooks.deliveries('script:dev-b:notify')).toEqual([]);
  });

  it('records a dropped delivery when the machine is offline', () => {
    const f = setup();
    f.control.detach(f.link);
    f.emit(ended);
    const [d] = f.hooks.deliveries(ID) ?? [];
    expect(d).toMatchObject({ state: 'dropped', error: 'offline' });
  });

  it('fails what is waiting when the link drops', () => {
    const f = setup();
    f.emit(ended);
    f.control.detach(f.link);
    const [d] = f.hooks.deliveries(ID) ?? [];
    expect(d).toMatchObject({ state: 'failed', error: 'connection-lost' });
  });

  it('refuses edits other than the switch', async () => {
    const f = setup();
    expect(await f.hooks.update(ID, { label: 'x' })).toEqual({
      ok: false,
      error: 'script-managed',
    });
    expect(await f.hooks.update('script:dev-a:nope', { enabled: false })).toEqual({
      ok: false,
      error: 'not-found',
    });
  });

  it('keeps the switch across hellos and forgets a hook that is not announced', async () => {
    const f = setup();
    await f.hooks.update(ID, { enabled: false });
    f.store.replaceScriptHooks('dev-a', [announce({ runs: 5 })], T0 + 1);
    expect(f.hooks.list()[0]).toMatchObject({ enabled: false, runs: 5 });
    f.store.replaceScriptHooks('dev-a', [], T0 + 2);
    expect(f.hooks.list()).toEqual([]);
  });

  it('tests a hook, even a disabled one, and audits it with the machine', async () => {
    const f = setup();
    await f.hooks.update(ID, { enabled: false });
    const tested = await f.hooks.test(ID);
    expect(tested?.delivery).toMatchObject({ state: 'pending', type: 'hook.test' });
    const [sent] = hookEvents(f.link);
    expect(sent?.event.type).toBe('hook.test');
    expect(sent?.event.session.project).toEqual({ name: '' });
    const audit = f.store.listAudit({}, { limit: 20 });
    const entries = JSON.stringify(audit);
    expect(entries).toContain('"tested"');
    expect(entries).toContain(ID);
    expect(entries).not.toContain('/opt/notify.sh');
  });

  it('settles what was pending when the core started', () => {
    const f = setup();
    f.emit(ended);
    const g = createHooks(f.store, {
      onEvent: () => () => {},
      onHookResult: () => () => {},
      onDevicesGone: () => () => {},
      ownerOnline: () => false,
    } as unknown as Control);
    g.start();
    stops.push(() => g.stop());
    expect(g.deliveries(ID)?.[0]).toMatchObject({ state: 'failed', error: 'interrupted' });
  });
});
