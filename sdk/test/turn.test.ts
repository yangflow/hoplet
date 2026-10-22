import { beforeEach, describe, expect, it } from 'vitest';
import { Hoplet, HopletError } from '../src/index.ts';
import { judge } from '../src/turn.ts';
import {
  FakeWebSocket,
  FakeWebSocketImpl,
  fakeFetch,
  type Handler,
  sessionScript,
  ticketHandler,
  until,
} from './support.ts';

const since = { sessionId: 's1', lineCount: 5, state: 'ready' } as const;

function client(...handlers: Handler[]) {
  const { fetch, calls } = fakeFetch(ticketHandler, ...handlers);
  const hoplet = new Hoplet({
    url: 'http://core.test',
    token: 't',
    fetch,
    WebSocket: FakeWebSocketImpl,
  });
  return { hoplet, calls };
}

beforeEach(() => FakeWebSocket.reset());

describe('judge', () => {
  it('waits for a start after the baseline', () => {
    expect(judge({ state: 'ready', lineCount: 5 }, false, { since })).toEqual({
      started: false,
      outcome: null,
    });
  });

  it('counts appended lines as a start, and a busy state too', () => {
    expect(judge({ state: 'ready', lineCount: 7 }, false, { since }).outcome).toBe('ok');
    expect(judge({ state: 'running', lineCount: 5 }, false, { since })).toEqual({
      started: true,
      outcome: null,
    });
  });

  it('returns at once for a session that is not busy when there is no baseline', () => {
    expect(judge({ state: 'ready', lineCount: 5 }, false, {}).outcome).toBe('ok');
    expect(judge({ state: 'failed', lineCount: 5 }, false, {}).outcome).toBe('failed');
    expect(judge({ state: 'offline', lineCount: 5 }, false, {}).outcome).toBe('ended');
    expect(judge({ state: 'idle', lineCount: 5 }, false, {}).outcome).toBe('ended');
    expect(judge({ state: 'external', lineCount: 5 }, false, {}).outcome).toBe('ended');
  });

  it('blocks on a permission only when asked', () => {
    expect(judge({ state: 'waiting-permission', lineCount: 5 }, false, {}).outcome).toBeNull();
    expect(
      judge({ state: 'waiting-permission', lineCount: 5 }, false, { untilBlocked: true }).outcome,
    ).toBe('blocked');
  });
});

describe('waitForTurn by polling', () => {
  const poll = { pollIntervalMs: 5 };

  it('ends ok when the turn runs and the session is ready', async () => {
    const { hoplet } = client(sessionScript(['ready', 5], ['running', 5], ['ready', 9]));
    const result = await hoplet.waitForTurn('s1', { since, ...poll });
    expect(result).toEqual({ result: 'ok', state: 'ready', lineCount: 9 });
  });

  it('ends failed with the runtime error', async () => {
    const { hoplet } = client(sessionScript(['running', 5], ['failed', 6, 'boom']));
    const result = await hoplet.waitForTurn('s1', { since, ...poll });
    expect(result.result).toBe('failed');
    expect(result.error).toBe('boom');
  });

  it('ends ended when the runtime goes offline', async () => {
    const { hoplet } = client(sessionScript(['running', 5], ['offline', 5]));
    expect((await hoplet.waitForTurn('s1', { since, ...poll })).result).toBe('ended');
  });

  it('ends blocked on a permission request with untilBlocked', async () => {
    const { hoplet } = client(sessionScript(['running', 5], ['waiting-permission', 6]));
    const result = await hoplet.waitForTurn('s1', { since, untilBlocked: true, ...poll });
    expect(result.result).toBe('blocked');
    expect(result.state).toBe('waiting-permission');
  });

  it('times out when the turn never starts', async () => {
    const { hoplet } = client(sessionScript(['ready', 5]));
    const t0 = Date.now();
    const result = await hoplet.waitForTurn('s1', { since, startTimeout: 40, ...poll });
    expect(result.result).toBe('timeout');
    expect(Date.now() - t0).toBeLessThan(2000);
  });

  it('times out in all when the turn keeps running', async () => {
    const { hoplet } = client(sessionScript(['running', 5]));
    const result = await hoplet.waitForTurn('s1', { since, timeout: 40, ...poll });
    expect(result.result).toBe('timeout');
    expect(result.state).toBe('running');
  });

  it('returns at once without a baseline for a session that is ready', async () => {
    const { hoplet } = client(sessionScript(['ready', 5]));
    expect((await hoplet.waitForTurn('s1')).result).toBe('ok');
  });

  it('survives a failed read and keeps waiting', async () => {
    let n = 0;
    const flaky = fakeFetch(ticketHandler, sessionScript(['running', 5], ['ready', 6]));
    const fetchImpl = ((input: string | URL | Request, init?: RequestInit) =>
      String(input).endsWith('/api/sessions/s1') && n++ === 1
        ? Promise.reject(new TypeError('blip'))
        : flaky.fetch(input, init)) as typeof fetch;
    const hoplet = new Hoplet({ url: 'http://core.test', token: 't', fetch: fetchImpl });
    expect((await hoplet.waitForTurn('s1', { since, ...poll })).result).toBe('ok');
  });

  it('throws an error that is not a network failure', async () => {
    const { hoplet } = client((call) =>
      call.path === '/api/sessions/s1'
        ? { status: 404, json: { code: 'session-not-found' } }
        : undefined,
    );
    await expect(hoplet.waitForTurn('s1', poll)).rejects.toMatchObject({
      code: 'session-not-found',
    });
  });

  it('aborts with the aborted code', async () => {
    const { hoplet } = client(sessionScript(['running', 5]));
    const controller = new AbortController();
    const waiting = hoplet.waitForTurn('s1', { since, signal: controller.signal, ...poll });
    setTimeout(() => controller.abort(), 20);
    const err = await waiting.catch((e: unknown) => e);
    expect(err).toBeInstanceOf(HopletError);
    expect((err as HopletError).code).toBe('aborted');
  });
});

describe('waitForTurn with drain', () => {
  const queue = (items: number, extra: object = {}): Handler => {
    let left = items;
    return (call) =>
      call.path === '/api/sessions/s1/queue'
        ? {
            json: {
              items: Array.from({ length: left-- > 0 ? left + 1 : 0 }, (_, i) => ({
                id: `q${i}`,
                text: 'next',
                attachments: [],
                version: 1,
                state: 'queued',
              })),
              ...extra,
            },
          }
        : undefined;
  };

  it('waits until the queue is empty', async () => {
    const { hoplet, calls } = client(sessionScript(['ready', 6]), queue(2));
    const result = await hoplet.waitForTurn('s1', { drain: true, pollIntervalMs: 5 });
    expect(result.result).toBe('ok');
    expect(calls.filter((c) => c.path.endsWith('/queue')).length).toBeGreaterThanOrEqual(3);
  });

  it('fails when the queue paused on an error', async () => {
    const { hoplet } = client(
      sessionScript(['ready', 6]),
      queue(1, { paused: 'error', error: 'queue broke' }),
    );
    const result = await hoplet.waitForTurn('s1', { drain: true, pollIntervalMs: 5 });
    expect(result.result).toBe('failed');
    expect(result.error).toBe('queue broke');
  });

  it('ends when the queue paused on an interrupt', async () => {
    const { hoplet } = client(sessionScript(['ready', 6]), queue(1, { paused: 'interrupted' }));
    expect((await hoplet.waitForTurn('s1', { drain: true, pollIntervalMs: 5 })).result).toBe(
      'ended',
    );
  });
});

describe('waitForTurn with events', () => {
  it('reads again when the core says the session changed', async () => {
    const { hoplet, calls } = client(sessionScript(['running', 5], ['ready', 8]));
    const waiting = hoplet.waitForTurn('s1', { since });
    await until(() => FakeWebSocket.instances.length === 1);
    const socket = FakeWebSocket.instances[0] as FakeWebSocket;
    socket.open();
    await until(() => calls.filter((c) => c.path === '/api/sessions/s1').length >= 1);
    socket.message({
      v: 15,
      type: 'session.appended',
      sessionId: 's1',
      appendedLines: 3,
      totalLines: 8,
      byteSize: 80,
      updatedAt: 3,
    });
    // The default poll is seconds away; the event must have woken the wait.
    const result = await waiting;
    expect(result.result).toBe('ok');
    expect(socket.closed).toBe(true);
  });
});
