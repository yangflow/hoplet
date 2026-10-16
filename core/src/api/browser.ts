/**
 * The browser WebSocket `/ws`: one upgrade per ticket (api/tickets.ts), then the events control publishes.
 * Runtime state and permission requests go to every browser; streamed output only to the browsers subscribed
 * to its session. Tool calls and permission requests carry the kind of their tool as the session provider's
 * mapping classifies it. Nothing buffers without bound: a browser that falls behind is dropped and reconnects.
 */
import type { IncomingMessage, Server } from 'node:http';
import {
  BrowserMessageSchema,
  type ErrorCode,
  PROTOCOL_VERSION,
  type ServerMessage,
  safeParse,
} from '@hoplet/shared';
import { type WebSocket, WebSocketServer } from 'ws';
import type { Control } from '../control/index.ts';
import { log } from '../log/index.ts';
import { classifyTool } from './projection/index.ts';
import type { Tickets } from './tickets.ts';

/**
 * Bytes a browser connection may have waiting to be written. A browser that reads slower than the core
 * produces is dropped at this point; it reconnects and reloads its state.
 */
export const MAX_BROWSER_BUFFER_BYTES = 1024 * 1024;

/** The part of a socket that sending to a browser needs. */
export interface BrowserOutlet {
  readyState: number;
  bufferedAmount: number;
  send(data: string): void;
  terminate(): void;
}

const OPEN = 1;

/** Sessions one browser may be subscribed to at once. */
export const MAX_SUBSCRIPTIONS = 256;

/** Sends `payload` unless the browser already has too much waiting, in which case it is dropped. */
export function sendToBrowser(ws: BrowserOutlet, payload: string): void {
  if (ws.readyState !== OPEN) return;
  if (ws.bufferedAmount > MAX_BROWSER_BUFFER_BYTES) {
    log('warn', 'api', 'browser too slow, dropped', { buffered: ws.bufferedAmount });
    ws.terminate();
    return;
  }
  ws.send(payload);
}

/** A tool call or permission request with its tool's kind; any other event as it is. */
function withToolKind(control: Control, msg: ServerMessage): ServerMessage {
  if (msg.type !== 'session.permission' && msg.type !== 'session.output') return msg;
  if (msg.type === 'session.output' && msg.kind !== 'tool-call') return msg;
  if (msg.tool === undefined) return msg;
  const provider = control.providerOf(msg.sessionId);
  if (!provider) return msg;
  const { kind, mcp } = classifyTool(provider, msg.tool);
  return { ...msg, toolKind: kind, ...(mcp ? { mcp } : {}) };
}

export function attachBrowserSocket(server: Server, control: Control, tickets: Tickets): void {
  const wss = new WebSocketServer({ noServer: true, maxPayload: 64 * 1024 });
  const alive = new Map<WebSocket, boolean>();
  const subscriptions = new Map<WebSocket, Set<string>>();

  /** Forgets a socket: its subscriptions no longer count as sessions open in a tab (ADR 0021). */
  function drop(ws: WebSocket): void {
    for (const sessionId of subscriptions.get(ws) ?? []) control.viewing.remove(sessionId);
    alive.delete(ws);
    subscriptions.delete(ws);
  }

  function reply(ws: WebSocket, msg: ServerMessage): void {
    sendToBrowser(ws, JSON.stringify(msg));
  }

  function replyError(ws: WebSocket, code: ErrorCode, message: string): void {
    reply(ws, { v: PROTOCOL_VERSION, type: 'error', code, message });
  }

  function sendRuntime(ws: WebSocket, sessionId: string): void {
    const runtime = control.runtimeOf(sessionId);
    reply(ws, {
      v: PROTOCOL_VERSION,
      type: 'session.runtime',
      sessionId,
      state: runtime.state,
      ...(runtime.error !== undefined ? { error: runtime.error } : {}),
      ...(runtime.capabilities ? { capabilities: runtime.capabilities } : {}),
    });
  }

  /** What a browser needs to start from when it subscribes: the state and the open permission requests. */
  function snapshot(ws: WebSocket, sessionId: string): void {
    sendRuntime(ws, sessionId);
    for (const permission of control.openPermissions(sessionId)) {
      reply(ws, withToolKind(control, permission));
    }
  }

  function handleMessage(ws: WebSocket, text: string): void {
    let raw: unknown;
    try {
      raw = JSON.parse(text);
    } catch {
      replyError(ws, 'invalid-json', 'invalid json');
      return;
    }
    const version = (raw as { v?: unknown } | null)?.v;
    if (version !== PROTOCOL_VERSION) {
      replyError(ws, 'protocol-version-mismatch', `this core speaks ${PROTOCOL_VERSION}`);
      return;
    }
    const parsed = safeParse(BrowserMessageSchema, raw);
    if (!parsed.success) {
      replyError(ws, 'invalid-message', 'invalid message');
      return;
    }
    const msg = parsed.output;
    const subscribed = subscriptions.get(ws) ?? new Set<string>();
    subscriptions.set(ws, subscribed);
    if (msg.type === 'unsubscribe') {
      if (subscribed.delete(msg.sessionId)) control.viewing.remove(msg.sessionId);
      return;
    }
    if (!subscribed.has(msg.sessionId) && subscribed.size >= MAX_SUBSCRIPTIONS) {
      replyError(ws, 'too-large', 'too many subscriptions');
      return;
    }
    if (!subscribed.has(msg.sessionId)) {
      subscribed.add(msg.sessionId);
      control.viewing.add(msg.sessionId);
    }
    snapshot(ws, msg.sessionId);
  }

  server.on('upgrade', (req: IncomingMessage, socket, head) => {
    const url = new URL(req.url ?? '/', `http://${req.headers.host ?? 'localhost'}`);
    if (url.pathname !== '/ws') return;
    if (!tickets.redeem(url.searchParams.get('ticket') ?? '')) {
      socket.write('HTTP/1.1 401 Unauthorized\r\nconnection: close\r\n\r\n');
      socket.destroy();
      return;
    }
    wss.handleUpgrade(req, socket, head, (ws) => wss.emit('connection', ws, req));
  });

  wss.on('connection', (ws: WebSocket) => {
    alive.set(ws, true);
    log('info', 'api', 'browser connected', { browsers: alive.size });
    ws.on('pong', () => alive.set(ws, true));
    ws.on('message', (data, isBinary) => {
      if (!isBinary) handleMessage(ws, data.toString());
    });
    ws.on('close', () => drop(ws));
    ws.on('error', () => drop(ws));
  });

  control.subscribe((event: ServerMessage) => {
    const msg = withToolKind(control, event);
    const payload = JSON.stringify(msg);
    for (const ws of alive.keys()) {
      if (msg.type === 'session.output' && !subscriptions.get(ws)?.has(msg.sessionId)) continue;
      sendToBrowser(ws, payload);
      // A subscribed session may have lost or regained its owner: say what it is now.
      if (msg.type === 'device.presence') {
        for (const sessionId of subscriptions.get(ws) ?? []) sendRuntime(ws, sessionId);
      }
    }
  });

  setInterval(() => {
    for (const [ws, isAlive] of alive) {
      if (!isAlive) {
        ws.terminate();
        drop(ws);
        continue;
      }
      alive.set(ws, false);
      ws.ping();
    }
  }, 30_000).unref();
}
