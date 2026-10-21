import { mkdtempSync, rmSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { PROTOCOL_VERSION, type ServerMessage } from '@hoplet/shared';
import { afterEach, describe, expect, it } from 'vitest';
import { MAX_BROWSER_BUFFER_BYTES, sendToBrowser } from '../src/api/browser.ts';
import { createApi } from '../src/api/index.ts';
import { routes } from '../src/api/routes.ts';
import { BulkBusyError, createControl } from '../src/control/index.ts';
import { createLink, type Outlet } from '../src/ingest/conn.ts';
import { Store } from '../src/store/index.ts';

const dirs: string[] = [];
const servers: Server[] = [];

afterEach(async () => {
  for (const s of servers.splice(0)) await new Promise<void>((r) => s.close(() => r()));
  for (const d of dirs.splice(0)) rmSync(d, { recursive: true, force: true });
});

function newStore(): Store {
  const dir = mkdtempSync(path.join(tmpdir(), 'hoplet-lanes-'));
  dirs.push(dir);
  return new Store(dir);
}

const deferred = () => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { gate, release };
};

describe('the bulk queue', () => {
  it('runs at most `concurrency` tasks and takes the rest in order', async () => {
    const control = createControl(newStore(), { bulk: { concurrency: 2, maxWaiting: 8 } });
    const started: number[] = [];
    const gates = [deferred(), deferred(), deferred(), deferred()];
    const done = gates.map((g, i) =>
      control.bulk.run(async () => {
        started.push(i);
        await g.gate;
        return i;
      }),
    );
    await new Promise((r) => setTimeout(r, 20));
    expect(started).toEqual([0, 1]);
    gates[0]?.release();
    await new Promise((r) => setTimeout(r, 20));
    expect(started).toEqual([0, 1, 2]);
    for (const g of gates) g.release();
    expect(await Promise.all(done)).toEqual([0, 1, 2, 3]);
    expect(control.bulk.size).toBe(0);
  });

  it('refuses work beyond the waiting bound', async () => {
    const control = createControl(newStore(), { bulk: { concurrency: 1, maxWaiting: 1 } });
    const { gate, release } = deferred();
    const first = control.bulk.run(() => gate);
    const second = control.bulk.run(() => 'second');
    await expect(control.bulk.run(() => 'third')).rejects.toBeInstanceOf(BulkBusyError);
    release();
    await first;
    expect(await second).toBe('second');
  });

  it('starts a task on a later turn, after work that was already waiting', async () => {
    const control = createControl(newStore());
    const order: string[] = [];
    setImmediate(() => order.push('control'));
    const task = control.bulk.run(() => order.push('bulk'));
    order.push('queued');
    await task;
    expect(order).toEqual(['queued', 'control', 'bulk']);
  });
});

describe('REST lanes', () => {
  it('puts history reads, searches and image uploads on the bulk lane and everything else on control', () => {
    const bulk = routes.filter((r) => r.lane === 'bulk').map((r) => r.path);
    expect(bulk.sort()).toEqual([
      '/api/devices/:deviceId/attachments',
      '/api/search',
      '/api/sessions/:sessionId/blocks/:line/:block',
      '/api/sessions/:sessionId/lines',
      '/api/sessions/:sessionId/search',
      '/api/sessions/:sessionId/snapshot/changes',
      '/api/sessions/:sessionId/snapshot/diff',
      '/api/sessions/:sessionId/usage',
      '/api/usage',
    ]);
    for (const path of [
      '/api/sessions',
      '/api/sessions/:sessionId/prompt',
      '/api/sessions/:sessionId/workspace',
      '/api/sessions/:sessionId/workspace/file',
      '/api/sessions/:sessionId/workspace/diff',
      '/api/sessions/:sessionId/handoff',
      '/api/sessions/:sessionId/handoff/cancel',
      '/api/budgets',
      '/api/budgets/:id',
      '/api/budgets/:id/allow',
    ]) {
      expect(routes.find((r) => r.path === path)?.lane, path).toBe('control');
    }
    expect(
      routes.find((r) => r.method === 'DELETE' && r.path === '/api/budgets/:id/allow')?.lane,
    ).toBe('control');
  });

  it('answers a control request while the bulk queue is full, and refuses more bulk with busy', async () => {
    const store = newStore();
    store.append(
      { sessionId: 's1', provider: 'claude', projectDir: 'p', filePath: '/x', deviceId: 'd' },
      ['{"n":1}'],
    );
    const control = createControl(store, { bulk: { concurrency: 1, maxWaiting: 0 } });
    const config = { port: 0, dataDir: '', token: 'host', webDir: null };
    const server = createServer();
    createApi(store, config, control).attach(server);
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    servers.push(server);
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const get = (p: string) => fetch(base + p, { headers: { authorization: 'Bearer host' } });

    const { gate, release } = deferred();
    const occupying = control.bulk.run(() => gate);

    const sessions = await get('/api/sessions');
    expect(sessions.status).toBe(200);
    const lines = await get('/api/sessions/s1/lines');
    expect(lines.status).toBe(409);
    expect(await lines.json()).toEqual({ code: 'busy' });

    release();
    await occupying;
    expect((await get('/api/sessions/s1/lines')).status).toBe(200);
  });
});

describe('store scans', () => {
  it('give the event loop back between chunks', async () => {
    const store = newStore();
    const lines = Array.from({ length: 500 }, (_, i) => JSON.stringify({ n: i, text: 'filler' }));
    for (let s = 0; s < 4; s++) {
      for (let c = 0; c < 3; c++) {
        store.append(
          {
            sessionId: `s${s}`,
            provider: 'claude',
            projectDir: 'p',
            filePath: '/x',
            deviceId: 'd',
          },
          lines,
        );
      }
    }
    await store.idle();
    let ticks = 0;
    let running = true;
    const spin = () => {
      if (!running) return;
      ticks++;
      setImmediate(spin);
    };
    setImmediate(spin);
    const result = await store.searchAll('no-such-text', 0, 10);
    running = false;
    expect(result.exhausted).toBe(true);
    // 12 chunks across 4 sessions: at least one turn of the loop for each.
    expect(ticks).toBeGreaterThanOrEqual(12);
  });
});

/** A socket that records what is written and lets a test finish each write. */
function fakeOutlet() {
  const sent: string[] = [];
  const callbacks: ((err?: Error) => void)[] = [];
  const outlet = {
    readyState: 1,
    bufferedAmount: 0,
    terminated: false,
    send(data: string, cb?: (err?: Error) => void) {
      sent.push(data);
      if (cb) callbacks.push(cb);
    },
    terminate() {
      outlet.terminated = true;
    },
  };
  return { outlet: outlet satisfies Outlet, sent, finishWrite: () => callbacks.shift()?.() };
}

const types = (sent: string[]) => sent.map((s) => (JSON.parse(s) as { type: string }).type);

describe('connection lanes', () => {
  const apply = {
    v: PROTOCOL_VERSION,
    type: 'handoff.data',
    sessionId: 's1',
    handoffId: 'h1',
    part: 'session',
    seq: 0,
    last: false,
    encoding: 'base64',
    data: 'AA==',
  } as const satisfies ServerMessage;
  const prompt = {
    v: PROTOCOL_VERSION,
    type: 'runtime.prompt',
    sessionId: 's1',
    promptId: 'p1',
    text: 'hi',
  } as const satisfies ServerMessage;

  it('writes a control message at once and one bulk message at a time', () => {
    const { outlet, sent, finishWrite } = fakeOutlet();
    const link = createLink(outlet);
    link.send(apply);
    link.send({ ...apply, seq: 1 });
    link.send(prompt);
    // The second bulk message waits for the first; the prompt does not.
    expect(types(sent)).toEqual(['handoff.data', 'runtime.prompt']);
    finishWrite();
    expect(types(sent)).toEqual(['handoff.data', 'runtime.prompt', 'handoff.data']);
  });

  it('drops a browser that has too much waiting, and sends to one that keeps up', () => {
    const { outlet, sent } = fakeOutlet();
    sendToBrowser(outlet, 'one');
    expect(sent).toEqual(['one']);
    outlet.bufferedAmount = MAX_BROWSER_BUFFER_BYTES + 1;
    sendToBrowser(outlet, 'two');
    expect(sent).toEqual(['one']);
    expect(outlet.terminated).toBe(true);
  });
});
