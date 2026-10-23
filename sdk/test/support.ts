import { type LiveSessionMeta, PROTOCOL_VERSION, type RuntimeState } from '@hoplet/shared';

export type Call = { method: string; path: string; body: unknown; authorization: string | null };
export type Reply = { status?: number; json?: unknown; text?: string };
export type Handler = (call: Call) => Reply | undefined;

/** A fetch that answers from `handlers` (first one to return a reply wins) and records every call. */
export function fakeFetch(...handlers: Handler[]): { fetch: typeof fetch; calls: Call[] } {
  const calls: Call[] = [];
  const impl = async (input: string | URL | Request, init?: RequestInit): Promise<Response> => {
    const url = new URL(String(input));
    const headers = new Headers(init?.headers);
    const call: Call = {
      method: init?.method ?? 'GET',
      path: `${url.pathname}${url.search}`,
      body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
      authorization: headers.get('authorization'),
    };
    calls.push(call);
    for (const handler of [health, ...handlers]) {
      const reply = handler(call);
      if (!reply) continue;
      const status = reply.status ?? 200;
      const body = reply.text ?? JSON.stringify(reply.json ?? {});
      return new Response(body, { status });
    }
    return new Response(JSON.stringify({ code: 'not-found' }), { status: 404 });
  };
  return { fetch: impl as typeof fetch, calls };
}

const health: Handler = (call) =>
  call.path === '/api/health'
    ? {
        json: {
          ok: true,
          sessions: 0,
          uptimeSec: 1,
          coreVersion: '0.0.0',
          protocolVersion: PROTOCOL_VERSION,
        },
      }
    : undefined;

export function session(
  state: RuntimeState,
  lineCount: number,
  extra: Partial<LiveSessionMeta['runtime']> = {},
  overrides: Partial<LiveSessionMeta> = {},
): LiveSessionMeta {
  return {
    sessionId: 's1',
    projectPath: '/work/app',
    projectDir: '-work-app',
    deviceName: 'laptop',
    ownerDeviceId: 'dev1',
    ownerEpoch: 1,
    title: 'A session',
    provider: 'claude',
    createdAt: 1,
    updatedAt: 2,
    lineCount,
    byteSize: lineCount * 10,
    parentId: null,
    childCount: 0,
    ownerConnected: true,
    runtime: { state, ...extra },
    ...overrides,
  };
}

/** The session a script of observations walks through: each read returns the next one, the last repeats. */
export function sessionScript(...steps: [RuntimeState, number, string?][]): Handler {
  let i = 0;
  return (call) => {
    if (call.method !== 'GET' || call.path !== '/api/sessions/s1') return undefined;
    const step = steps[Math.min(i++, steps.length - 1)];
    if (!step) return undefined;
    const [state, lines, error] = step;
    return { json: session(state, lines, error === undefined ? {} : { error }) };
  };
}

type Listener = (event: unknown) => void;

/** A WebSocket the test drives by hand. */
export class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  static reset(): void {
    FakeWebSocket.instances = [];
  }

  readonly url: string;
  readonly sent: unknown[] = [];
  closed = false;
  private readonly listeners = new Map<string, Listener[]>();

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type: string, listener: Listener): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
  }

  send(data: string): void {
    this.sent.push(JSON.parse(data));
  }

  close(code = 1000): void {
    this.closed = true;
    this.emit('close', { code });
  }

  open(): void {
    this.emit('open', {});
  }

  message(payload: unknown): void {
    this.emit('message', { data: typeof payload === 'string' ? payload : JSON.stringify(payload) });
  }

  serverClose(code: number): void {
    this.emit('close', { code });
  }

  private emit(type: string, event: unknown): void {
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}

export const FakeWebSocketImpl = FakeWebSocket as unknown as typeof WebSocket;

/** Resolves once `predicate` holds, checking on every tick. */
export async function until(predicate: () => boolean, label = 'condition'): Promise<void> {
  for (let i = 0; i < 400; i++) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 5));
  }
  throw new Error(`timed out waiting for ${label}`);
}

export const ticketHandler: Handler = (call) =>
  call.method === 'POST' && call.path === '/api/ws-tickets'
    ? { json: { ticket: 't-1', expiresInSec: 30 } }
    : undefined;
