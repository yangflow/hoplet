import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import type { UsageQuery, UsageResponse } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { createApi } from '../src/api/index.ts';
import { createControl } from '../src/control/index.ts';
import { Store } from '../src/store/index.ts';
import { extractUsage } from '../src/store/usage-lines.ts';

const HOST = 'host-token-0123456789abcdef';
const dirs: string[] = [];
const servers: Server[] = [];
afterEach(async () => {
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});
const tmp = () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-usage-'));
  dirs.push(dir);
  return dir;
};

const T0 = Date.parse('2026-10-05T09:00:00.000Z');
const DAY = 86_400_000;

const claude = (
  id: string,
  model: string,
  u: { i?: number; o?: number; w?: number; r?: number },
  at = T0,
) =>
  JSON.stringify({
    type: 'assistant',
    timestamp: new Date(at).toISOString(),
    message: {
      id,
      model,
      role: 'assistant',
      content: [{ type: 'text', text: 'x' }],
      usage: {
        input_tokens: u.i ?? 0,
        output_tokens: u.o ?? 0,
        cache_creation_input_tokens: u.w ?? 0,
        cache_read_input_tokens: u.r ?? 0,
      },
    },
  });

const codexContext = (model: string) =>
  JSON.stringify({
    type: 'turn_context',
    timestamp: new Date(T0).toISOString(),
    payload: { model },
  });
const codexCount = (
  total: number,
  last: { input: number; cached: number; output: number },
  at = T0,
) =>
  JSON.stringify({
    type: 'event_msg',
    timestamp: new Date(at).toISOString(),
    payload: {
      type: 'token_count',
      info: {
        total_token_usage: { total_tokens: total },
        last_token_usage: {
          input_tokens: last.input,
          cached_input_tokens: last.cached,
          output_tokens: last.output,
        },
      },
    },
  });

const input = (sessionId: string, provider: 'claude' | 'codex' = 'claude', deviceId = 'dev-a') => ({
  sessionId,
  provider,
  projectDir: 'proj',
  filePath: '/x',
  deviceId,
});
const query = (over: Partial<UsageQuery> = {}): UsageQuery => ({
  groupBy: ['model'],
  utcOffset: 0,
  offset: 0,
  limit: 100,
  ...over,
});

describe('extractUsage', () => {
  it('keeps one record, the largest counts, for a Claude message written as several lines', () => {
    const scan = extractUsage(
      'claude',
      [
        claude('m1', 'claude-opus-5-5', { i: 3, o: 5, w: 100, r: 7 }),
        claude('m1', 'claude-opus-5-5', { i: 3, o: 40, w: 100, r: 7 }),
        claude('m2', 'claude-opus-5-5', { i: 1, o: 2 }),
      ],
      '',
      0,
    );
    expect(scan.records).toEqual([
      expect.objectContaining({
        messageId: 'm1',
        input: 3,
        output: 40,
        cacheWrite: 100,
        cacheRead: 7,
      }),
      expect.objectContaining({ messageId: 'm2', input: 1, output: 2 }),
    ]);
  });

  it('skips synthetic, zero and unreadable lines', () => {
    const scan = extractUsage(
      'claude',
      [
        claude('s1', '<synthetic>', { i: 9 }),
        claude('z1', 'claude-opus-5-5', {}),
        '{"usage": not json',
        JSON.stringify({ type: 'user', message: { role: 'user', content: 'hi' } }),
      ],
      '',
      0,
    );
    expect(scan.records).toEqual([]);
  });

  it('maps Codex token counts to the turn model, splitting cached input out', () => {
    const scan = extractUsage(
      'codex',
      [
        codexContext('gpt-x'),
        codexCount(100, { input: 80, cached: 30, output: 20 }),
        codexCount(100, { input: 80, cached: 30, output: 20 }),
        codexCount(260, { input: 100, cached: 0, output: 60 }),
        JSON.stringify({ type: 'event_msg', payload: { type: 'token_count', info: null } }),
      ],
      '',
      0,
    );
    expect(scan.model).toBe('gpt-x');
    expect(scan.records).toEqual([
      expect.objectContaining({
        messageId: 'c100',
        model: 'gpt-x',
        input: 50,
        cacheRead: 30,
        output: 20,
      }),
      expect.objectContaining({ messageId: 'c260', model: 'gpt-x', input: 100, output: 60 }),
    ]);
  });

  it('carries the Codex model into the next batch', () => {
    const first = extractUsage('codex', [codexContext('gpt-y')], '', 0);
    const next = extractUsage(
      'codex',
      [codexCount(5, { input: 3, cached: 0, output: 2 })],
      first.model,
      0,
    );
    expect(next.records[0]?.model).toBe('gpt-y');
  });
});

describe('usage recorded with the append', () => {
  it('upserts by message across appends and stays one row per message', () => {
    const store = new Store(tmp());
    store.append(input('s1'), [claude('m1', 'claude-opus-5-5', { i: 1, o: 5 })]);
    store.append(input('s1'), [
      claude('m1', 'claude-opus-5-5', { i: 1, o: 30 }),
      claude('m2', 'claude-opus-5-5', { i: 2, o: 3, r: 10 }),
    ]);
    const r = store.usage(query());
    expect(r.total).toMatchObject({ input: 3, output: 33, cacheRead: 10, messages: 2 });
    expect(store.sessionUsage('s1')).toMatchObject({ complete: true, total: { messages: 2 } });
  });

  it('keeps the Codex model across appends', () => {
    const store = new Store(tmp());
    store.append(input('c1', 'codex'), [codexContext('gpt-z')]);
    store.append(input('c1', 'codex'), [codexCount(9, { input: 5, cached: 1, output: 4 })]);
    expect(store.usage(query()).rows[0]?.keys[0]?.value).toBe('gpt-z');
  });

  it('rolls usage back with the append when the transaction fails', () => {
    const dir = tmp();
    const store = new Store(dir);
    store.append(input('s1'), [claude('m1', 'claude-opus-5-5', { i: 1 })]);
    const db = new DatabaseSync(path.join(dir, 'hoplet.db'));
    db.exec(
      `CREATE TRIGGER usage_fail BEFORE INSERT ON usage WHEN NEW.message_id = 'boom'
       BEGIN SELECT RAISE(ABORT, 'boom'); END`,
    );
    db.close();
    expect(() =>
      store.append(input('s1'), [
        claude('m2', 'claude-opus-5-5', { i: 4 }),
        claude('boom', 'claude-opus-5-5', { i: 8 }),
      ]),
    ).toThrow();
    expect(store.usage(query()).total).toMatchObject({ input: 1, messages: 1 });
    expect(store.getSession('s1')?.lineCount).toBe(1);
  });

  it('is removed with the session', () => {
    const store = new Store(tmp());
    store.append(input('s1'), [claude('m1', 'claude-opus-5-5', { i: 1 })]);
    store.purgeSession('s1');
    expect(store.usage(query()).total.messages).toBe(0);
  });
});

describe('backfill', () => {
  function seed(dir: string) {
    const store = new Store(dir);
    store.append(input('old1'), [
      claude('m1', 'claude-opus-5-5', { i: 1, o: 2 }),
      claude('m2', 'claude-opus-5-5', { i: 3, o: 4 }),
    ]);
    store.append(input('old2', 'codex'), [
      codexContext('gpt-z'),
      codexCount(10, { input: 6, cached: 2, output: 4 }),
    ]);
    // The state of a store that existed before usage was recorded.
    const db = new DatabaseSync(path.join(dir, 'hoplet.db'));
    db.exec(`DELETE FROM usage; UPDATE sessions SET usage_backfilled = 0, usage_model = ''`);
    db.close();
  }

  it('reads sessions recorded before, marks them done and is idempotent', async () => {
    const dir = tmp();
    seed(dir);
    const store = new Store(dir);
    expect(store.usage(query()).backfilling).toBe(true);
    expect(store.sessionUsage('old1')?.complete).toBe(false);
    store.startUsageBackfill();
    await store.idle();
    const r = store.usage(query({ groupBy: ['session'] }));
    expect(r.backfilling).toBe(false);
    expect(r.total).toMatchObject({
      input: 3 + 1 + 4,
      output: 2 + 4 + 4,
      cacheRead: 2,
      messages: 3,
    });
    expect(store.sessionUsage('old2')?.models[0]?.keys[0]?.value).toBe('gpt-z');
    store.startUsageBackfill();
    await store.idle();
    expect(store.usage(query()).total.messages).toBe(3);
  });

  it('agrees with live appends made before it ran and resumes after a restart', async () => {
    const dir = tmp();
    seed(dir);
    const store = new Store(dir);
    store.append(input('old1'), [
      claude('m3', 'claude-opus-5-5', { i: 10 }),
      claude('m1', 'claude-opus-5-5', { i: 1, o: 2 }),
    ]);
    store.startUsageBackfill();
    await store.idle();
    expect(store.usage(query()).total).toMatchObject({ input: 1 + 3 + 10 + 4, messages: 4 });
    const again = new Store(dir);
    again.startUsageBackfill();
    await again.idle();
    expect(again.usage(query()).total.messages).toBe(4);
  });
});

describe('queries', () => {
  function fill() {
    const store = new Store(tmp());
    store.mintDevice('A machine', 'dev-a');
    store.mintDevice('B machine', 'dev-b');
    store.append(input('s1', 'claude', 'dev-a'), [
      claude('m1', 'claude-opus-5-5', { i: 1_000_000, o: 100_000 }, T0),
      claude('m2', 'claude-sonnet-5-5', { i: 500_000, r: 1_000_000 }, T0 + DAY),
    ]);
    store.append({ ...input('s2', 'claude', 'dev-b'), projectDir: 'other' }, [
      claude('m3', 'claude-mystery-1', { i: 7, o: 7 }, T0 + DAY),
    ]);
    return store;
  }

  it('groups by model with estimated cost, and leaves unknown models without a cost', () => {
    const r = fill().usage(query());
    expect(r.estimated).toBe(true);
    const by = Object.fromEntries(r.rows.map((x) => [x.keys[0]?.value, x]));
    // opus 5.5: 1M input at $4 + 100K output at $20 per million.
    expect(by['claude-opus-5-5']?.estimatedCostUsd).toBeCloseTo(4 + 2);
    // sonnet 5.5: 500K input at $2 + 1M cache read at $0.20.
    expect(by['claude-sonnet-5-5']?.estimatedCostUsd).toBeCloseTo(1 + 0.2);
    expect(by['claude-mystery-1']).toMatchObject({
      estimatedCostUsd: null,
      costComplete: false,
      input: 7,
    });
    expect(r.total.costComplete).toBe(false);
    expect(r.total.estimatedCostUsd).toBeCloseTo(7.2);
    expect(r.rowCount).toBe(3);
  });

  it('groups by day, session, device and project and by two levels', () => {
    const store = fill();
    expect(store.usage(query({ groupBy: ['day'] })).rows.map((x) => x.keys[0]?.value)).toEqual([
      '2026-10-05',
      '2026-10-06',
    ]);
    expect(store.usage(query({ groupBy: ['session'] })).rows[0]?.keys[0]?.value).toBe('s1');
    expect(
      store
        .usage(query({ groupBy: ['device'] }))
        .rows.map((x) => x.keys[0]?.label)
        .sort(),
    ).toEqual(['A machine', 'B machine']);
    expect(store.usage(query({ groupBy: ['project'] })).rowCount).toBe(2);
    const two = store.usage(query({ groupBy: ['device', 'model'] }));
    expect(two.rowCount).toBe(3);
    expect(two.rows[0]?.keys).toHaveLength(2);
  });

  it("buckets days at the caller's UTC offset", () => {
    const store = new Store(tmp());
    store.append(input('s1'), [
      claude('m1', 'claude-opus-5-5', { i: 1, o: 1 }, Date.parse('2026-10-20T20:00:00Z')),
    ]);
    const day = (utcOffset?: number) =>
      store.usage(query({ groupBy: ['day'], ...(utcOffset === undefined ? {} : { utcOffset }) }))
        .rows[0]?.keys[0]?.value;
    expect(day()).toBe('2026-10-20');
    expect(day(480)).toBe('2026-10-21');
    expect(day(-300)).toBe('2026-10-20');
  });

  it('filters by range, session, device, project and model, and pages', () => {
    const store = fill();
    expect(store.usage(query({ from: T0 + DAY })).total.messages).toBe(2);
    expect(store.usage(query({ to: T0 + DAY })).total.messages).toBe(1);
    expect(store.usage(query({ session: 's2' })).total.messages).toBe(1);
    expect(store.usage(query({ device: 'dev-a' })).total.messages).toBe(2);
    expect(store.usage(query({ project: 'other' })).total.messages).toBe(1);
    expect(store.usage(query({ model: 'claude-opus-5-5' })).total.messages).toBe(1);
    const page = store.usage(query({ limit: 1, offset: 1 }));
    expect(page.rows).toHaveLength(1);
    expect(page.rowCount).toBe(3);
  });

  it('applies a price override, also to dated variants, and drops it when omitted', () => {
    const store = fill();
    store.append(input('s3'), [claude('m9', 'claude-mystery-1-20260101', { i: 1_000_000 })]);
    const price = { input: 3, output: 4, cacheWrite: 5, cacheRead: 6 };
    store.setUsagePrices({ overrides: { 'claude-mystery-1': price } });
    expect(store.usagePrices().overrides).toEqual({ 'claude-mystery-1': price });
    expect(store.usagePrices().defaults['claude-opus-5-5']).toBeDefined();
    const r = store.usage(query({ model: 'claude-mystery-1-20260101' }));
    expect(r.total.estimatedCostUsd).toBeCloseTo(3);
    store.setUsagePrices({
      overrides: { 'claude-opus-5-5': { input: 1, output: 1, cacheWrite: 1, cacheRead: 1 } },
    });
    expect(store.usage(query({ model: 'claude-mystery-1' })).total.estimatedCostUsd).toBeNull();
    expect(store.usage(query({ model: 'claude-opus-5-5' })).total.estimatedCostUsd).toBeCloseTo(
      1.1,
    );
  });
});

describe('/api/usage', () => {
  async function boot() {
    const dir = tmp();
    const store = new Store(dir);
    store.append(input('s1'), [claude('m1', 'claude-opus-5-5', { i: 1_000_000 }, T0)]);
    const server = createServer();
    createApi(
      store,
      { port: 0, dataDir: dir, token: HOST, webDir: null },
      createControl(store, {}),
    ).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const port = (server.address() as AddressInfo).port;
    return async (method: string, url: string, body?: unknown, auth = true) => {
      const res = await fetch(`http://127.0.0.1:${port}${url}`, {
        method,
        headers: {
          ...(auth ? { authorization: `Bearer ${HOST}` } : {}),
          'content-type': 'application/json',
        },
        body: body === undefined ? null : JSON.stringify(body),
      });
      const text = await res.text();
      return { status: res.status, body: text ? JSON.parse(text) : {} };
    };
  }

  it('needs the host token', async () => {
    const call = await boot();
    for (const [m, u] of [
      ['GET', '/api/usage'],
      ['GET', '/api/sessions/s1/usage'],
      ['GET', '/api/usage/prices'],
      ['PUT', '/api/usage/prices'],
    ] as const) {
      expect((await call(m, u, m === 'PUT' ? { overrides: {} } : undefined, false)).status).toBe(
        401,
      );
    }
  });

  it('answers usage with filters and groupBy, and the usage of one session', async () => {
    const call = await boot();
    const r = await call('GET', `/api/usage?groupBy=day,model&from=${T0}&model=claude-opus-5-5`);
    expect(r.status).toBe(200);
    const body = r.body as UsageResponse;
    expect(body).toMatchObject({ estimated: true, groupBy: ['day', 'model'], rowCount: 1 });
    expect(body.rows[0]?.estimatedCostUsd).toBeCloseTo(4);
    const s = await call('GET', '/api/sessions/s1/usage');
    expect(s.body).toMatchObject({ sessionId: 's1', complete: true, estimated: true });
    expect((await call('GET', '/api/sessions/nope/usage')).status).toBe(404);
  });

  it('refuses a bad groupBy', async () => {
    const call = await boot();
    for (const g of ['bogus', 'day,day', 'day,model,session', '']) {
      expect((await call('GET', `/api/usage?groupBy=${g}`)).status).toBe(400);
    }
  });

  it('stores price overrides and answers them with the defaults', async () => {
    const call = await boot();
    const price = { input: 1, output: 2, cacheWrite: 3, cacheRead: 4 };
    const put = await call('PUT', '/api/usage/prices', { overrides: { m: price } });
    expect(put.body.overrides).toEqual({ m: price });
    expect(Object.keys((await call('GET', '/api/usage/prices')).body.defaults)).toContain(
      'claude-opus-5-5',
    );
    expect(
      (await call('PUT', '/api/usage/prices', { overrides: { m: { input: -1 } } })).status,
    ).toBe(400);
  });
});
