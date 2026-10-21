import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import {
  type BudgetBody,
  type BudgetListResponse,
  MAX_BUDGETS,
  type NotifyPayload,
  PROTOCOL_VERSION,
  type ServerMessage,
} from '@hoplet/shared';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApi } from '../src/api/index.ts';
import { type ControlReply, createControl, type DeviceLink } from '../src/control/index.ts';
import { createNotify } from '../src/notify/index.ts';
import { periodBounds } from '../src/store/budgets.ts';
import { Store } from '../src/store/index.ts';

const HOST = 'host-token-0123456789abcdef';
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

const tmp = () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-budgets-'));
  dirs.push(dir);
  return dir;
};

// Monday 2026-10-05, 09:00 UTC.
const T0 = Date.parse('2026-10-05T09:00:00.000Z');
const HOUR = 3_600_000;
const DAY = 86_400_000;
const APP = '/work/app';
const OTHER = '/work/other';

const usageLine = (id: string, model: string, input: number, at: number, cwd = APP) =>
  JSON.stringify({
    type: 'assistant',
    cwd,
    timestamp: new Date(at).toISOString(),
    message: {
      id,
      model,
      role: 'assistant',
      content: [{ type: 'text', text: 'x' }],
      usage: {
        input_tokens: input,
        output_tokens: 0,
        cache_creation_input_tokens: 0,
        cache_read_input_tokens: 0,
      },
    },
  });
const userLine = (cwd: string) =>
  JSON.stringify({ type: 'user', cwd, message: { role: 'user', content: 'hello' } });

const meta = (sessionId: string, deviceId: string) => ({
  sessionId,
  provider: 'claude' as const,
  projectDir: 'p',
  filePath: `/x/${sessionId}`,
  deviceId,
});

const OPUS = 'claude-opus-5-5'; // 4 USD per million input tokens

const body = (over: Partial<BudgetBody> = {}): BudgetBody => ({
  name: 'Budget',
  scope: { kind: 'global', value: '' },
  period: 'day',
  unit: 'tokens',
  amount: 1000,
  alertPercent: 80,
  onLimit: 'warn',
  utcOffset: 0,
  ...over,
});

/** s1: app on dev-a; s2: other on dev-b. */
function seeded(at = T0) {
  const dir = tmp();
  const store = new Store(dir);
  store.append(meta('s1', 'dev-a'), [userLine(APP), usageLine('m1', OPUS, 400_000, at)]);
  store.append(meta('s2', 'dev-b'), [userLine(OTHER), usageLine('m2', OPUS, 100_000, at, OTHER)]);
  return { dir, store };
}

describe('period bounds', () => {
  const iso = (ms: number) => new Date(ms).toISOString();

  it('cuts days at the offset', () => {
    const at = Date.parse('2026-10-05T23:30:00Z');
    expect(iso(periodBounds('day', 0, at).start)).toBe('2026-10-05T00:00:00.000Z');
    expect(iso(periodBounds('day', 0, at).end)).toBe('2026-10-06T00:00:00.000Z');
    // 23:30 UTC is already the 6th at +60, and still the 5th at -300.
    expect(iso(periodBounds('day', 60, at).start)).toBe('2026-10-05T23:00:00.000Z');
    expect(iso(periodBounds('day', -300, at).start)).toBe('2026-10-05T05:00:00.000Z');
  });

  it('starts a week on Monday, at the offset', () => {
    const sunday = Date.parse('2026-10-11T23:30:00Z');
    expect(iso(periodBounds('week', 0, sunday).start)).toBe('2026-10-05T00:00:00.000Z');
    expect(iso(periodBounds('week', 0, sunday).end)).toBe('2026-10-12T00:00:00.000Z');
    // At +60 it is Monday 00:30 already: a new week.
    expect(iso(periodBounds('week', 60, sunday).start)).toBe('2026-10-11T23:00:00.000Z');
    const monday = Date.parse('2026-10-05T00:00:00Z');
    expect(periodBounds('week', 0, monday).start).toBe(monday);
    expect(iso(periodBounds('week', 0, monday - 1).start)).toBe('2026-09-28T00:00:00.000Z');
    // Wednesday 1970-01-07 (before the epoch's first Monday) still lands on a Monday.
    expect(iso(periodBounds('week', 0, Date.parse('1970-01-01T00:00:00Z')).start)).toBe(
      '1969-12-29T00:00:00.000Z',
    );
  });

  it('cuts months at the offset and across year ends', () => {
    const at = Date.parse('2026-11-01T02:00:00Z');
    expect(iso(periodBounds('month', 0, at).start)).toBe('2026-11-01T00:00:00.000Z');
    expect(iso(periodBounds('month', 0, at).end)).toBe('2026-12-01T00:00:00.000Z');
    // At -300 it is still 21:00 on 31 October.
    expect(iso(periodBounds('month', -300, at).start)).toBe('2026-10-01T05:00:00.000Z');
    expect(iso(periodBounds('month', -300, at).end)).toBe('2026-11-01T05:00:00.000Z');
    expect(iso(periodBounds('month', 0, Date.parse('2026-12-31T23:59:59Z')).end)).toBe(
      '2027-01-01T00:00:00.000Z',
    );
  });
});

describe('spend', () => {
  it('sums by scope within the period', () => {
    const { store } = seeded();
    // Outside the day: yesterday's message does not count.
    store.append(meta('s1', 'dev-a'), [usageLine('old', OPUS, 7_000_000, T0 - DAY)]);
    const make = (b: Partial<BudgetBody>) => store.createBudget(body(b), T0).spent;
    expect(make({})).toBe(500_000);
    expect(make({ scope: { kind: 'project', value: APP } })).toBe(400_000);
    expect(make({ scope: { kind: 'project', value: OTHER } })).toBe(100_000);
    expect(make({ scope: { kind: 'device', value: 'dev-b' } })).toBe(100_000);
    expect(make({ scope: { kind: 'device', value: 'nobody' } })).toBe(0);
    // Tuesday: Monday's usage is in the week but not the day.
    store.append(meta('s1', 'dev-a'), [usageLine('tue', OPUS, 1_000_000, T0 + DAY)]);
    expect(store.createBudget(body(), T0 + DAY).spent).toBe(1_000_000);
    expect(store.createBudget(body({ period: 'week' }), T0 + DAY).spent).toBe(1_500_000);
  });

  it('counts cost from priced usage and says when it is incomplete', () => {
    const { store } = seeded();
    const priced = store.createBudget(body({ unit: 'cost', amount: 10 }), T0);
    expect(priced.spent).toBeCloseTo(2); // 500k tokens at 4 USD per million
    expect(priced.costComplete).toBe(true);
    store.append(meta('s1', 'dev-a'), [usageLine('u', 'mystery-model-1', 9_000_000, T0)]);
    const after = store.getBudget(priced.id, T0);
    expect(after?.spent).toBeCloseTo(2);
    expect(after?.costComplete).toBe(false);
    // Tokens count every model.
    expect(store.createBudget(body(), T0).spent).toBe(9_500_000);
    expect(store.createBudget(body(), T0).costComplete).toBe(true);
  });

  it('reports the state from the alert line and the limit', () => {
    const { store } = seeded();
    const at = (amount: number) => store.createBudget(body({ amount }), T0);
    expect(at(1_000_000)).toMatchObject({ state: 'ok', percent: 50, paused: false });
    expect(at(600_000)).toMatchObject({ state: 'alert' });
    expect(at(500_000)).toMatchObject({ state: 'over', percent: 100, paused: false });
    expect(at(500_000).periodEnd - at(500_000).periodStart).toBe(DAY);
  });
});

describe('the pause', () => {
  it('blocks a covering scope only for a pause budget that is over', () => {
    const { store } = seeded();
    const warn = store.createBudget(body({ amount: 100 }), T0);
    expect(store.budgetBlockForSession('s1', T0)).toBeNull();
    store.updateBudget(warn.id, body({ amount: 100, onLimit: 'pause' }), T0);
    expect(store.budgetBlockForSession('s1', T0)).toEqual({ id: warn.id, name: 'Budget' });
    store.deleteBudget(warn.id);
    const proj = store.createBudget(
      body({ name: 'App', scope: { kind: 'project', value: APP }, amount: 100, onLimit: 'pause' }),
      T0,
    );
    expect(store.budgetBlockForSession('s1', T0)?.id).toBe(proj.id);
    expect(store.budgetBlockForSession('s2', T0)).toBeNull();
    const dev = store.createBudget(
      body({ scope: { kind: 'device', value: 'dev-b' }, amount: 100, onLimit: 'pause' }),
      T0,
    );
    expect(store.budgetBlockForSession('s2', T0)?.id).toBe(dev.id);
    expect(store.budgetBlockFor(OTHER, 'dev-b', T0)?.id).toBe(dev.id);
    expect(store.budgetBlockFor('/elsewhere', 'dev-c', T0)).toBeNull();
    // The next period starts empty.
    expect(store.budgetBlockForSession('s1', T0 + DAY)).toBeNull();
  });

  it('is lifted until the period ends and then applies again', () => {
    const { store } = seeded();
    const b = store.createBudget(body({ amount: 100, onLimit: 'pause' }), T0);
    expect(store.allowBudget(b.id, T0)).toMatchObject({ state: 'allowed', paused: false });
    expect(store.budgetBlockForSession('s1', T0 + HOUR)).toBeNull();
    // A message in the next day goes over again, and the lift is gone.
    store.append(meta('s1', 'dev-a'), [usageLine('next', OPUS, 500, T0 + DAY)]);
    expect(store.budgetBlockForSession('s1', T0 + DAY)?.id).toBe(b.id);
    expect(store.allowBudget('nope', T0)).toBeNull();
  });

  it('keeps a lift across a restart, and drops it when the amount changes', () => {
    const { dir, store } = seeded();
    const b = store.createBudget(body({ amount: 100, onLimit: 'pause' }), T0);
    store.allowBudget(b.id, T0);
    const again = new Store(dir);
    expect(again.budgetBlockForSession('s1', T0)).toBeNull();
    again.updateBudget(b.id, body({ amount: 200, onLimit: 'pause' }), T0);
    expect(again.budgetBlockForSession('s1', T0)?.id).toBe(b.id);
    // A rename alone changes nothing.
    again.allowBudget(b.id, T0);
    again.updateBudget(b.id, body({ name: 'Renamed', amount: 200, onLimit: 'pause' }), T0);
    expect(again.budgetBlockForSession('s1', T0)).toBeNull();
  });
});

describe('the pause cache', () => {
  const pauseBody = body({ amount: 600_000, onLimit: 'pause' });

  it('answers a prompt without touching the database while the cache is warm', () => {
    const { store } = seeded();
    const b = store.createBudget(pauseBody, T0);
    const db = (store as unknown as { ctx: { db: { prepare: () => unknown } } }).ctx.db;
    const prepare = vi.spyOn(db, 'prepare');
    expect(store.budgetBlockFor(APP, 'dev-a', T0 + HOUR)).toBeNull();
    expect(store.budgetBlockFor('/elsewhere', 'dev-c', T0 + HOUR)).toBeNull();
    expect(prepare).not.toHaveBeenCalled();
    // Only a budget whose period has rolled over is recomputed, and only until the next call.
    expect(store.budgetBlockFor(APP, 'dev-a', T0 + DAY)).toBeNull();
    expect(prepare).toHaveBeenCalled();
    prepare.mockClear();
    expect(store.budgetBlockFor(APP, 'dev-a', T0 + DAY + HOUR)).toBeNull();
    expect(prepare).not.toHaveBeenCalled();
    expect(b.paused).toBe(false);
  });

  it('blocks after usage is recorded, once the debounce has passed', () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'Date'] });
    try {
      vi.setSystemTime(T0);
      const { store } = seeded();
      const b = store.createBudget(pauseBody, T0);
      expect(store.budgetBlockForSession('s1', T0)).toBeNull();
      store.append(meta('s1', 'dev-a'), [usageLine('more', OPUS, 200_000, T0)]);
      expect(store.budgetBlockForSession('s1', T0)).toBeNull();
      vi.advanceTimersByTime(2000);
      expect(store.budgetBlockForSession('s1', T0)).toEqual({ id: b.id, name: 'Budget' });
      // A budget the owner changed or removed leaves the cache at once.
      store.deleteBudget(b.id);
      expect(store.budgetBlockForSession('s1', T0)).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it('clears the pause when the period rolls over, without new usage', () => {
    const { dir, store } = seeded();
    const b = store.createBudget(body({ amount: 100, onLimit: 'pause' }), T0);
    expect(store.budgetBlockFor(APP, 'dev-a', T0)?.id).toBe(b.id);
    expect(store.budgetBlockFor(APP, 'dev-a', T0 + DAY)).toBeNull();
    // The cache is rebuilt when the store opens.
    expect(new Store(dir).budgetBlockFor(APP, 'dev-a', T0)?.id).toBe(b.id);
  });
});

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

const runtimeState = (state: string) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'runtime.state',
    sessionId: 's1',
    state,
    capabilities: { permissions: true, interrupt: true, steer: true, config: [] },
  }) as ControlReply;

describe('control', () => {
  function setup() {
    const { store } = seeded();
    const clock = { now: T0 };
    const control = createControl(store, { now: () => clock.now, promptTimeoutMs: 300 });
    const link = fakeLink();
    control.attach('dev-a', link);
    control.receive(link, runtimeState('idle'));
    const prompts = () => link.sent.filter((m) => m.type === 'runtime.prompt');
    // The agent takes a prompt by reporting the turn as running.
    const send = async (text: string, id: string) => {
      const p = control.prompt('s1', 'dev-a', text, id);
      await new Promise((r) => setTimeout(r, 10));
      control.receive(link, runtimeState('running'));
      const r = await p;
      control.receive(link, runtimeState('idle'));
      return r;
    };
    return { store, control, link, clock, prompts, send };
  }

  it('refuses a prompt over a pause budget and names it', async () => {
    const t = setup();
    const b = t.store.createBudget(body({ amount: 100, onLimit: 'pause' }), T0);
    const r = await t.control.prompt('s1', 'dev-a', 'go', 'p1');
    expect(r).toMatchObject({
      ok: false,
      error: 'over-budget',
      budget: { id: b.id, name: 'Budget' },
    });
    expect(t.prompts()).toHaveLength(0);
  });

  it('does not refuse over a warn budget', async () => {
    const t = setup();
    t.store.createBudget(body({ amount: 100 }), T0);
    expect((await t.send('go', 'p1')).ok).toBe(true);
    expect(t.prompts()).toHaveLength(1);
  });

  it('prompts again once the pause is lifted', async () => {
    const t = setup();
    const b = t.store.createBudget(body({ amount: 100, onLimit: 'pause' }), T0);
    expect((await t.control.prompt('s1', 'dev-a', 'go', 'p1')).ok).toBe(false);
    t.store.allowBudget(b.id, T0);
    expect((await t.send('go', 'p2')).ok).toBe(true);
  });

  it('does not touch a running turn when a budget goes over', () => {
    const t = setup();
    t.control.receive(t.link, runtimeState('running'));
    t.store.createBudget(body({ amount: 100, onLimit: 'pause' }), T0);
    expect(t.link.sent).toEqual([]);
  });

  it('pauses the queue instead of retrying', async () => {
    const t = setup();
    t.store.createBudget(body({ amount: 100, onLimit: 'pause' }), T0);
    t.control.queueAdd('s1', { id: 'a', text: 'queued', attachments: [] });
    await vi.waitFor(() =>
      expect(t.control.queueOf('s1')).toMatchObject({ paused: 'error', error: 'over-budget' }),
    );
    expect(t.prompts()).toHaveLength(0);
    const items = t.control.queueOf('s1').items;
    expect(items).toHaveLength(1);
    expect(items[0]?.state).toBe('queued');
    await new Promise((r) => setTimeout(r, 40));
    expect(t.prompts()).toHaveLength(0);
  });
});

describe('notifications', () => {
  async function receiver() {
    const got: NotifyPayload[] = [];
    const server = createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on('data', (c: Buffer) => chunks.push(c));
      req.on('end', () => {
        got.push(JSON.parse(Buffer.concat(chunks).toString()) as NotifyPayload);
        res.writeHead(200);
        res.end();
      });
    });
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    return { got, url: `http://127.0.0.1:${(server.address() as AddressInfo).port}/h` };
  }

  function start(store: Store, clock: { now: number }) {
    const control = createControl(store, { now: () => clock.now });
    const notify = createNotify(store, control, {
      publicUrl: null,
      now: () => clock.now,
      retryDelayMs: 5,
      timeoutMs: 1_000,
      budgetDebounceMs: 3_600_000,
    });
    notify.start();
    stops.push(() => notify.stop());
    return notify;
  }

  it('sends the alert and the limit once each, and not again after a restart', async () => {
    const { dir, store } = seeded();
    const hook = await receiver();
    store.addWebhook({ url: hook.url, label: 'h' }, T0);
    const clock = { now: T0 };
    const b = store.createBudget(body({ name: 'Daily', amount: 600_000, onLimit: 'pause' }), T0);
    const notify = start(store, clock);

    await notify.evaluateBudgets();
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    expect(hook.got[0]).toMatchObject({
      kind: 'budget',
      project: 'Daily',
      budget: { id: b.id, name: 'Daily', threshold: 'alert', pause: true },
    });
    expect(hook.got[0]?.budget?.percent).toBeCloseTo(83.3, 0);

    await notify.evaluateBudgets();
    await new Promise((r) => setTimeout(r, 30));
    expect(hook.got).toHaveLength(1);

    store.append(meta('s1', 'dev-a'), [usageLine('m3', OPUS, 200_000, T0)]);
    await notify.evaluateBudgets();
    await vi.waitFor(() => expect(hook.got).toHaveLength(2));
    expect(hook.got[1]?.budget).toMatchObject({ threshold: 'limit' });
    await notify.evaluateBudgets();

    // A restart on the same data sends nothing again.
    notify.stop();
    const restarted = new Store(dir);
    const again = start(restarted, clock);
    await again.evaluateBudgets();
    await new Promise((r) => setTimeout(r, 30));
    expect(hook.got).toHaveLength(2);

    // The next period sends afresh.
    clock.now = T0 + DAY;
    restarted.append(meta('s1', 'dev-a'), [usageLine('m4', OPUS, 700_000, T0 + DAY)]);
    await again.evaluateBudgets();
    await vi.waitFor(() => expect(hook.got).toHaveLength(3));
    expect(hook.got[2]?.budget).toMatchObject({ threshold: 'limit' });
  });

  it('sends only the limit for a budget that is already over, and none when under the alert line', async () => {
    const { store } = seeded();
    const hook = await receiver();
    store.addWebhook({ url: hook.url, label: 'h' }, T0);
    store.createBudget(body({ name: 'Quiet', amount: 10_000_000 }), T0);
    store.createBudget(body({ name: 'Over', amount: 1000 }), T0);
    const notify = start(store, { now: T0 });
    await notify.evaluateBudgets();
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    await new Promise((r) => setTimeout(r, 30));
    expect(hook.got).toHaveLength(1);
    expect(hook.got[0]?.budget).toMatchObject({ name: 'Over', threshold: 'limit', pause: false });
  });

  it('evaluates after usage is recorded, debounced', async () => {
    const { store } = seeded();
    const hook = await receiver();
    store.addWebhook({ url: hook.url, label: 'h' }, T0);
    store.createBudget(body({ name: 'Live', amount: 650_000 }), T0);
    const control = createControl(store, { now: () => T0 });
    const notify = createNotify(store, control, {
      publicUrl: null,
      now: () => T0,
      budgetDebounceMs: 10,
    });
    notify.start();
    stops.push(() => notify.stop());
    // The first evaluation on start sees 500k of 650k: under the alert line.
    await new Promise((r) => setTimeout(r, 60));
    expect(hook.got).toHaveLength(0);
    store.append(meta('s1', 'dev-a'), [usageLine('m3', OPUS, 100_000, T0)]);
    await vi.waitFor(() => expect(hook.got).toHaveLength(1));
    expect(hook.got[0]?.budget?.threshold).toBe('alert');
  });
});

describe('/api/budgets', () => {
  async function boot() {
    const { dir, store } = seeded(Date.now());
    const control = createControl(store, { promptTimeoutMs: 300 });
    const link = fakeLink();
    const send = link.send;
    link.send = (m) => {
      send(m);
      if (m.type === 'runtime.prompt')
        queueMicrotask(() => control.receive(link, runtimeState('running')));
    };
    link.sendAndWait = async (m) => link.send(m);
    control.attach('dev-a', link);
    control.receive(link, runtimeState('idle'));
    const server = createServer();
    createApi(store, { port: 0, dataDir: dir, token: HOST, webDir: null }, control).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const port = (server.address() as AddressInfo).port;
    const call = async (method: string, url: string, payload?: unknown, auth = true) => {
      const res = await fetch(`http://127.0.0.1:${port}${url}`, {
        method,
        headers: {
          ...(auth ? { authorization: `Bearer ${HOST}` } : {}),
          'content-type': 'application/json',
        },
        body: payload === undefined ? null : JSON.stringify(payload),
      });
      const text = await res.text();
      return {
        status: res.status,
        body: (text ? JSON.parse(text) : {}) as Record<string, unknown>,
      };
    };
    return { call, store, link };
  }

  it('needs the host token', async () => {
    const { call } = await boot();
    for (const [m, u] of [
      ['GET', '/api/budgets'],
      ['POST', '/api/budgets'],
      ['PUT', '/api/budgets/x'],
      ['DELETE', '/api/budgets/x'],
      ['POST', '/api/budgets/x/allow'],
    ] as const) {
      expect((await call(m, u, m === 'GET' ? undefined : body(), false)).status, `${m} ${u}`).toBe(
        401,
      );
    }
  });

  it('creates, lists, updates and deletes, with the spend of the period', async () => {
    const { call } = await boot();
    const created = await call('POST', '/api/budgets', {
      name: ' Team ',
      scope: { kind: 'project', value: APP },
      period: 'month',
      unit: 'cost',
      amount: 10,
    });
    expect(created.status).toBe(201);
    expect(created.body).toMatchObject({
      name: 'Team',
      alertPercent: 80,
      onLimit: 'warn',
      utcOffset: 0,
      state: 'ok',
      paused: false,
      costComplete: true,
    });
    expect(created.body.spent).toBeCloseTo(1.6);
    expect(created.body.percent).toBeCloseTo(16);
    expect(Number(created.body.periodEnd)).toBeGreaterThan(Number(created.body.periodStart));
    const id = created.body.id as string;

    const list = await call('GET', '/api/budgets');
    const listed = list.body as unknown as BudgetListResponse;
    expect(listed.budgets.map((b) => b.id)).toEqual([id]);
    expect(listed.backfilling).toBe(false);

    const put = await call('PUT', `/api/budgets/${id}`, {
      name: 'Team',
      scope: { kind: 'project', value: APP },
      period: 'month',
      unit: 'cost',
      amount: 1,
      onLimit: 'pause',
    });
    expect(put.status).toBe(200);
    expect(put.body).toMatchObject({ state: 'over', paused: true, onLimit: 'pause' });
    expect((await call('PUT', '/api/budgets/missing', body())).status).toBe(404);

    const del = await call('DELETE', `/api/budgets/${id}`);
    expect(del.status).toBe(200);
    expect((del.body as unknown as BudgetListResponse).budgets).toEqual([]);
    expect((await call('DELETE', `/api/budgets/${id}`)).status).toBe(404);
  });

  it('refuses bad definitions', async () => {
    const { call } = await boot();
    const bad: unknown[] = [
      { ...body(), name: '' },
      { ...body(), name: 'x'.repeat(81) },
      { ...body(), amount: 0 },
      { ...body(), amount: -1 },
      { ...body(), amount: 1e13 },
      { ...body(), amount: 10.5 },
      { ...body(), alertPercent: 0 },
      { ...body(), alertPercent: 101 },
      { ...body(), period: 'year' },
      { ...body(), unit: 'euro' },
      { ...body(), onLimit: 'stop' },
      { ...body(), utcOffset: 841 },
      { ...body(), utcOffset: 0.5 },
      { ...body(), scope: { kind: 'global', value: 'x' } },
      { ...body(), scope: { kind: 'project', value: '' } },
      { ...body(), scope: { kind: 'planet', value: 'x' } },
    ];
    for (const b of bad) {
      expect((await call('POST', '/api/budgets', b)).status, JSON.stringify(b)).toBe(400);
    }
    // A fractional amount is fine in dollars.
    expect(
      (await call('POST', '/api/budgets', { ...body(), unit: 'cost', amount: 10.5 })).status,
    ).toBe(201);
  });

  it('keeps at most 100 budgets', async () => {
    const { call, store } = await boot();
    for (let i = 0; i < MAX_BUDGETS; i++) store.createBudget(body({ name: `b${i}` }), Date.now());
    const r = await call('POST', '/api/budgets', body());
    expect(r.status).toBe(413);
    expect(r.body.code).toBe('too-large');
  });

  it('refuses a prompt with over-budget and names the budget, until it is allowed', async () => {
    const { call, link } = await boot();
    const created = await call('POST', '/api/budgets', body({ amount: 100, onLimit: 'pause' }));
    const id = created.body.id as string;
    const refused = await call('POST', '/api/sessions/s1/prompt', { text: 'go', promptId: 'p1' });
    expect(refused.status).toBe(409);
    expect(refused.body).toMatchObject({ code: 'over-budget', budget: { id, name: 'Budget' } });
    expect(link.sent.filter((m) => m.type === 'runtime.prompt')).toHaveLength(0);

    const allowed = await call('POST', `/api/budgets/${id}/allow`);
    expect(allowed.status).toBe(200);
    expect(allowed.body).toMatchObject({ state: 'allowed', paused: false });
    const ok = await call('POST', '/api/sessions/s1/prompt', { text: 'go', promptId: 'p2' });
    expect(ok.status).toBe(202);
  });

  it('does not refuse a prompt over a warn budget, and allow is for pause budgets', async () => {
    const { call } = await boot();
    const created = await call('POST', '/api/budgets', body({ amount: 100 }));
    const ok = await call('POST', '/api/sessions/s1/prompt', { text: 'go', promptId: 'p1' });
    expect(ok.status).toBe(202);
    const allow = await call('POST', `/api/budgets/${created.body.id}/allow`);
    expect(allow.status).toBe(400);
    expect(allow.body).toMatchObject({ code: 'invalid-request', reason: 'onLimit' });
    expect((await call('POST', '/api/budgets/missing/allow')).status).toBe(404);
  });
});
