import { PROTOCOL_VERSION } from '@hoplet/shared';
import { beforeEach, describe, expect, it } from 'vitest';
import { Hoplet, type HopletError } from '../src/index.ts';
import { FakeWebSocket, FakeWebSocketImpl, fakeFetch, type Handler, until } from './support.ts';

let tickets = 0;
const tickets_: Handler = (call) =>
  call.method === 'POST' && call.path === '/api/ws-tickets'
    ? { json: { ticket: `t-${++tickets}`, expiresInSec: 30 } }
    : undefined;

function client(...handlers: Handler[]) {
  const { fetch, calls } = fakeFetch(...handlers);
  return {
    hoplet: new Hoplet({
      url: 'https://core.test:8443/',
      token: 'tok',
      fetch,
      WebSocket: FakeWebSocketImpl,
    }),
    calls,
  };
}

const runtime = (state: string) => ({
  v: PROTOCOL_VERSION,
  type: 'session.runtime',
  sessionId: 's1',
  state,
});

beforeEach(() => {
  FakeWebSocket.reset();
  tickets = 0;
});

describe('subscribe', () => {
  it('opens the socket with a ticket, subscribes on open and yields typed messages', async () => {
    const { hoplet, calls } = client(tickets_);
    const stream = hoplet.subscribe({ sessionIds: ['s1', 's2'] });
    await until(() => FakeWebSocket.instances.length === 1);
    const socket = FakeWebSocket.instances[0] as FakeWebSocket;
    expect(socket.url).toBe('wss://core.test:8443/ws?ticket=t-1');
    expect(calls.find((c) => c.path === '/api/ws-tickets')?.authorization).toBe('Bearer tok');
    socket.open();
    expect(socket.sent).toEqual([
      { v: PROTOCOL_VERSION, type: 'subscribe', sessionId: 's1' },
      { v: PROTOCOL_VERSION, type: 'subscribe', sessionId: 's2' },
    ]);
    socket.message(runtime('running'));
    expect(await stream.next()).toEqual({
      done: false,
      value: { type: 'sdk.connected', reconnected: false },
    });
    const second = await stream.next();
    expect(second.value).toMatchObject({ type: 'session.runtime', state: 'running' });
    stream.close();
    expect(await stream.next()).toEqual({ done: true, value: undefined });
  });

  it('maps http to ws', async () => {
    const { fetch } = fakeFetch(tickets_);
    const hoplet = new Hoplet({
      url: 'http://localhost:8787',
      token: 't',
      fetch,
      WebSocket: FakeWebSocketImpl,
    });
    const stream = hoplet.subscribe();
    await until(() => FakeWebSocket.instances.length === 1);
    expect(FakeWebSocket.instances[0]?.url).toBe('ws://localhost:8787/ws?ticket=t-1');
    stream.close();
  });

  it('drops bad JSON, other directions and invalid shapes', async () => {
    const { hoplet } = client(tickets_);
    const stream = hoplet.subscribe({ sessionIds: ['s1'] });
    await until(() => FakeWebSocket.instances.length === 1);
    const socket = FakeWebSocket.instances[0] as FakeWebSocket;
    socket.open();
    socket.message('not json');
    socket.message({ v: PROTOCOL_VERSION, type: 'subscribe', sessionId: 's1' });
    socket.message({ v: PROTOCOL_VERSION, type: 'nope' });
    socket.message({ v: PROTOCOL_VERSION, type: 'session.runtime', sessionId: 5 });
    socket.message(runtime('ready'));
    await stream.next();
    const event = await stream.next();
    expect(event.value).toMatchObject({ type: 'session.runtime', state: 'ready' });
    stream.close();
  });

  it('subscribes and unsubscribes more sessions on an open socket', async () => {
    const { hoplet } = client(tickets_);
    const stream = hoplet.subscribe();
    await until(() => FakeWebSocket.instances.length === 1);
    const socket = FakeWebSocket.instances[0] as FakeWebSocket;
    socket.open();
    stream.subscribe('s9');
    stream.subscribe('s9');
    stream.unsubscribe('s9');
    expect(socket.sent).toEqual([
      { v: PROTOCOL_VERSION, type: 'subscribe', sessionId: 's9' },
      { v: PROTOCOL_VERSION, type: 'unsubscribe', sessionId: 's9' },
    ]);
    stream.close();
  });

  it('reconnects with a new ticket and says so', async () => {
    const { hoplet } = client(tickets_);
    const stream = hoplet.subscribe({ sessionIds: ['s1'] });
    await until(() => FakeWebSocket.instances.length === 1);
    FakeWebSocket.instances[0]?.open();
    await stream.next();
    FakeWebSocket.instances[0]?.serverClose(1006);
    await until(() => FakeWebSocket.instances.length === 2);
    const second = FakeWebSocket.instances[1] as FakeWebSocket;
    expect(second.url).toContain('ticket=t-2');
    second.open();
    expect(second.sent).toHaveLength(1);
    expect((await stream.next()).value).toEqual({ type: 'sdk.connected', reconnected: true });
    stream.close();
  });

  it('fails with unauthorized on close code 4401', async () => {
    const { hoplet } = client(tickets_);
    const stream = hoplet.subscribe();
    await until(() => FakeWebSocket.instances.length === 1);
    FakeWebSocket.instances[0]?.open();
    FakeWebSocket.instances[0]?.serverClose(4401);
    await stream.next();
    const err = (await stream.next().catch((e: unknown) => e)) as HopletError;
    expect(err.code).toBe('unauthorized');
    expect(err.status).toBe(401);
  });

  it('fails with the core error when the ticket is refused', async () => {
    const { hoplet } = client((call) =>
      call.path === '/api/ws-tickets' ? { status: 401, json: { code: 'unauthorized' } } : undefined,
    );
    const stream = hoplet.subscribe();
    const err = (await stream.next().catch((e: unknown) => e)) as HopletError;
    expect(err.code).toBe('unauthorized');
    expect(FakeWebSocket.instances).toHaveLength(0);
  });

  it('needs a WebSocket implementation', () => {
    const hoplet = new Hoplet({ url: 'http://x', token: 't', fetch: fakeFetch().fetch });
    const original = globalThis.WebSocket;
    // Node may provide one globally; the check only applies when it does not.
    if (original === undefined) {
      expect(() => hoplet.subscribe()).toThrowError(/WebSocket/);
    } else {
      const stream = hoplet.subscribe();
      stream.close();
    }
  });
});
