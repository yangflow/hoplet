import {
  type GenericSchema,
  PROTOCOL_VERSION,
  type ServerMessage,
  safeParse,
  WIRE_MESSAGES,
  type WireType,
  wireTypes,
} from '@hoplet/shared';
import { HopletError } from './error.ts';

/** Sent by the SDK itself each time the socket is open and subscribed; `reconnected` is false the first time. */
export type ConnectedEvent = { type: 'sdk.connected'; reconnected: boolean };

/** What `subscribe` yields: a message the core sent the browser, or the SDK's own `sdk.connected`. */
export type HopletEvent = ServerMessage | ConnectedEvent;

/** The close code the core uses when a token expires or is revoked. */
const CLOSE_UNAUTHORIZED = 4401;
const BACKOFF_START_MS = 1000;
const BACKOFF_MAX_MS = 15000;

/** The messages a browser can receive; `error` is a device message the core also sends to browsers. */
const BROWSER_TYPES: ReadonlySet<string> = new Set<string>([...wireTypes('core-to-web'), 'error']);

export type EventStreamDeps = {
  /** Mints a single-use ticket (`POST /api/ws-tickets`) and returns the socket URL to open with it. */
  socketUrl: () => Promise<string>;
  WebSocketImpl: typeof WebSocket;
  sessionIds: readonly string[];
};

/**
 * A live stream of the core's messages for the sessions it is subscribed to. It reconnects with a new
 * ticket when the socket drops and yields `sdk.connected` after every (re)connection, so the consumer can
 * read the state again. It ends with an error when the token is refused. One consumer iterates it.
 */
export class EventStream implements AsyncIterable<HopletEvent>, AsyncIterator<HopletEvent> {
  private readonly deps: EventStreamDeps;
  private readonly ids: Set<string>;
  private readonly queue: HopletEvent[] = [];
  private waiter: (() => void) | null = null;
  private failure: HopletError | null = null;
  private closed = false;
  private socket: WebSocket | null = null;
  private open = false;
  private wake: (() => void) | null = null;

  constructor(deps: EventStreamDeps) {
    this.deps = deps;
    this.ids = new Set(deps.sessionIds);
    void this.run();
  }

  /** Starts receiving the events of one more session. */
  subscribe(sessionId: string): void {
    if (this.ids.has(sessionId)) return;
    this.ids.add(sessionId);
    this.send('subscribe', sessionId);
  }

  unsubscribe(sessionId: string): void {
    if (!this.ids.delete(sessionId)) return;
    this.send('unsubscribe', sessionId);
  }

  /** Ends the stream; an iteration waiting for an event finishes. */
  close(): void {
    this.closed = true;
    this.wake?.();
    this.socket?.close(1000);
    this.notify();
  }

  [Symbol.asyncIterator](): this {
    return this;
  }

  async next(): Promise<IteratorResult<HopletEvent>> {
    for (;;) {
      const event = this.queue.shift();
      if (event !== undefined) return { done: false, value: event };
      if (this.failure) throw this.failure;
      if (this.closed) return { done: true, value: undefined };
      await new Promise<void>((resolve) => {
        this.waiter = resolve;
      });
    }
  }

  async return(): Promise<IteratorResult<HopletEvent>> {
    this.close();
    return { done: true, value: undefined };
  }

  private notify(): void {
    const waiter = this.waiter;
    this.waiter = null;
    waiter?.();
  }

  private push(event: HopletEvent): void {
    this.queue.push(event);
    this.notify();
  }

  private fail(error: HopletError): void {
    this.failure = error;
    this.closed = true;
    this.notify();
  }

  private send(type: 'subscribe' | 'unsubscribe', sessionId: string): void {
    if (!this.open || !this.socket) return;
    this.socket.send(JSON.stringify({ v: PROTOCOL_VERSION, type, sessionId }));
  }

  private sleep(ms: number): Promise<void> {
    return new Promise((resolve) => {
      const timer = setTimeout(done, ms);
      this.wake = done;
      function done(): void {
        clearTimeout(timer);
        resolve();
      }
    });
  }

  private async run(): Promise<void> {
    let delay = BACKOFF_START_MS;
    let connectedBefore = false;
    while (!this.closed) {
      let url: string;
      try {
        url = await this.deps.socketUrl();
      } catch (err) {
        if (err instanceof HopletError && (err.status === 401 || err.status === 403)) {
          this.fail(err);
          return;
        }
        await this.sleep(delay);
        delay = Math.min(delay * 2, BACKOFF_MAX_MS);
        continue;
      }
      if (this.closed) return;
      const outcome = await this.connect(url, connectedBefore);
      if (outcome.opened) {
        connectedBefore = true;
        delay = BACKOFF_START_MS;
      }
      if (outcome.code === CLOSE_UNAUTHORIZED) {
        this.fail(
          new HopletError({
            status: 401,
            code: 'unauthorized',
            reason: 'The token expired or was revoked.',
          }),
        );
        return;
      }
      if (this.closed) return;
      await this.sleep(delay);
      delay = Math.min(delay * 2, BACKOFF_MAX_MS);
    }
  }

  /** Opens one socket; resolves when it closes. */
  private connect(url: string, reconnected: boolean): Promise<{ opened: boolean; code: number }> {
    return new Promise((resolve) => {
      let opened = false;
      const socket = new this.deps.WebSocketImpl(url);
      this.socket = socket;
      socket.addEventListener('open', () => {
        opened = true;
        this.open = true;
        for (const id of this.ids) this.send('subscribe', id);
        this.push({ type: 'sdk.connected', reconnected });
      });
      socket.addEventListener('message', (ev: MessageEvent) => {
        const event = parseMessage(ev.data);
        if (event) this.push(event);
      });
      socket.addEventListener('close', (ev: CloseEvent) => {
        this.open = false;
        if (this.socket === socket) this.socket = null;
        resolve({ opened, code: ev.code });
      });
    });
  }
}

/** A message from the core as a typed event; anything else (bad JSON, another direction, wrong shape) is dropped. */
function parseMessage(data: unknown): ServerMessage | null {
  if (typeof data !== 'string') return null;
  let raw: unknown;
  try {
    raw = JSON.parse(data);
  } catch {
    return null;
  }
  const type = (raw as { type?: unknown } | null)?.type;
  if (typeof type !== 'string' || !BROWSER_TYPES.has(type)) return null;
  const entry = WIRE_MESSAGES[type as WireType];
  const parsed = safeParse(entry.schema as GenericSchema, raw);
  return parsed.success ? (parsed.output as ServerMessage) : null;
}
