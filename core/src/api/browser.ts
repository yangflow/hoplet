/**
 * The browser WebSocket `/ws`: one upgrade per ticket (api/tickets.ts), then the events control publishes.
 * Runtime state and permission requests go to every browser; streamed output and rule decisions only to the browsers subscribed
 * to its session. Tool calls and permission requests carry the kind of their tool as the session provider's
 * mapping classifies it. Nothing buffers without bound: a browser that falls behind is dropped and reconnects.
 */
import type { IncomingMessage, Server } from 'node:http';
import {
  type BrowserMessage,
  BrowserMessageSchema,
  type ErrorCode,
  PROTOCOL_VERSION,
  type ServerMessage,
  safeParse,
} from '@hoplet/shared';
import { type WebSocket, WebSocketServer } from 'ws';
import type { Control, TerminalSink } from '../control/index.ts';
import { log } from '../log/index.ts';
import { type Principal, seesDevice, seesSession } from './principal.ts';
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

/** The close code of a connection whose API token expired or was revoked. */
export const TOKEN_ENDED_CLOSE_CODE = 4401;

/** How often a connection of an API token is checked against the token, besides its expiry timer and a revoke. */
const TOKEN_CHECK_MS = 30_000;

/**
 * Whether the principal receives the event. The host receives all of it. An API token receives only what concerns a
 * session or machine its grant reaches; an event that names neither is not for it.
 */
export function visibleTo(principal: Principal, control: Control, msg: ServerMessage): boolean {
  if (principal.kind !== 'token') return principal.kind === 'host';
  switch (msg.type) {
    case 'device.presence':
      return seesDevice(principal, msg.deviceId);
    case 'session.appended':
    case 'session.deleted':
    case 'session.updated':
    case 'session.runtime':
    case 'session.commands':
    case 'session.queue':
    case 'session.output':
    case 'session.permission':
    case 'session.rule':
    case 'session.listening':
    case 'handoff.progress':
      return seesSession(principal, control, msg.sessionId);
    case 'notify':
      return msg.sessionId !== undefined && seesSession(principal, control, msg.sessionId);
    default:
      return false;
  }
}

export interface BrowserSocketOptions {
  /** Whether the API token is still valid (not revoked, not expired). */
  tokenActive(tokenId: string): boolean;
  /** The clock. */
  now?: () => number;
}

export function attachBrowserSocket(
  server: Server,
  control: Control,
  tickets: Tickets,
  options: BrowserSocketOptions = { tokenActive: () => true },
): void {
  const wss = new WebSocketServer({ noServer: true, maxPayload: 64 * 1024 });
  const now = options.now ?? Date.now;
  const alive = new Map<WebSocket, boolean>();
  /** Who each connection acts as, from its ticket. */
  const principals = new Map<WebSocket, Principal>();
  const expiryTimers = new Map<WebSocket, NodeJS.Timeout>();
  const subscriptions = new Map<WebSocket, Set<string>>();
  /** The terminals each socket is attached to, and the sink that delivers their output to it (ADR 0026). */
  const attachments = new Map<WebSocket, { ids: Set<string>; sink: TerminalSink }>();

  /** Forgets a socket: its subscriptions no longer count as sessions open in a tab (ADR 0021). */
  function drop(ws: WebSocket): void {
    for (const sessionId of subscriptions.get(ws) ?? []) control.viewing.remove(sessionId);
    const attached = attachments.get(ws);
    if (attached)
      for (const terminalId of attached.ids) control.terminals.detach(terminalId, attached.sink);
    alive.delete(ws);
    subscriptions.delete(ws);
    attachments.delete(ws);
    principals.delete(ws);
    const timer = expiryTimers.get(ws);
    if (timer) clearTimeout(timer);
    expiryTimers.delete(ws);
  }

  /** Closes a connection whose token ended. */
  function endToken(ws: WebSocket): void {
    drop(ws);
    ws.close(TOKEN_ENDED_CLOSE_CODE, 'token ended');
    // A peer that does not answer the close handshake is cut after a moment.
    setTimeout(() => ws.terminate(), 2_000).unref();
  }

  tickets.onRevoke((tokenId) => {
    for (const [ws, principal] of [...principals]) {
      if (principal.kind === 'token' && principal.token.id === tokenId) endToken(ws);
    }
  });

  function attachmentsOf(ws: WebSocket): { ids: Set<string>; sink: TerminalSink } {
    let attached = attachments.get(ws);
    if (!attached) {
      attached = {
        ids: new Set(),
        sink: { send: (msg) => reply(ws, msg), buffered: () => ws.bufferedAmount },
      };
      attachments.set(ws, attached);
    }
    return attached;
  }

  function handleTerminal(
    ws: WebSocket,
    msg: Exclude<BrowserMessage, { type: 'subscribe' | 'unsubscribe' }>,
  ): void {
    const attached = attachmentsOf(ws);
    switch (msg.type) {
      case 'terminal.attach':
        if (attached.ids.has(msg.terminalId)) return;
        if (control.terminals.attach(msg.terminalId, attached.sink)) {
          attached.ids.add(msg.terminalId);
        } else {
          reply(ws, {
            v: PROTOCOL_VERSION,
            type: 'terminal.ended',
            terminalId: msg.terminalId,
            reason: 'closed',
          });
        }
        return;
      case 'terminal.detach':
        if (attached.ids.delete(msg.terminalId))
          control.terminals.detach(msg.terminalId, attached.sink);
        return;
      case 'terminal.keys':
        control.terminals.input(msg.terminalId, attached.sink, msg.data);
        return;
      case 'terminal.size':
        control.terminals.resize(msg.terminalId, attached.sink, msg.cols, msg.rows);
        return;
    }
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

  function sendListening(ws: WebSocket, sessionId: string): void {
    reply(ws, {
      v: PROTOCOL_VERSION,
      type: 'session.listening',
      sessionId,
      ...control.portsOf(sessionId),
    });
  }

  /** What a browser needs to start from when it subscribes: the state, the open permission requests and the ports. */
  function snapshot(ws: WebSocket, sessionId: string): void {
    sendRuntime(ws, sessionId);
    sendListening(ws, sessionId);
    for (const permission of control.openPermissions(sessionId)) {
      reply(ws, withToolKind(control, permission));
    }
    for (const line of control.ruleLinesOf(sessionId)) reply(ws, line);
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
    const principal = principals.get(ws);
    if (!principal) return;
    if (msg.type !== 'subscribe' && msg.type !== 'unsubscribe') {
      // The terminal is for the host (ADR 0033).
      if (principal.kind !== 'host') {
        replyError(ws, 'forbidden', 'the terminal is not available to this token');
        return;
      }
      handleTerminal(ws, msg);
      return;
    }
    const subscribed = subscriptions.get(ws) ?? new Set<string>();
    subscriptions.set(ws, subscribed);
    if (msg.type === 'unsubscribe') {
      if (subscribed.delete(msg.sessionId)) control.viewing.remove(msg.sessionId);
      return;
    }
    if (!seesSession(principal, control, msg.sessionId)) {
      // Out of the token's reach is answered as a session that does not exist.
      replyError(ws, 'session-not-found', 'session not found');
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
    const principal = tickets.redeem(url.searchParams.get('ticket') ?? '');
    if (!principal) {
      socket.write('HTTP/1.1 401 Unauthorized\r\nconnection: close\r\n\r\n');
      socket.destroy();
      return;
    }
    wss.handleUpgrade(req, socket, head, (ws) => {
      principals.set(ws, principal);
      wss.emit('connection', ws, req);
    });
  });

  wss.on('connection', (ws: WebSocket) => {
    alive.set(ws, true);
    const principal = principals.get(ws);
    if (principal?.kind === 'token') {
      // The connection ends with the token, whichever comes first: its expiry, a revoke (tickets.revoke) or a check.
      const left = principal.token.expiresAt - now();
      if (left <= 0) {
        endToken(ws);
        return;
      }
      // setTimeout cannot wait longer than 2^31 ms; the periodic check covers the rest.
      if (left < 2 ** 31 - 1) expiryTimers.set(ws, setTimeout(() => endToken(ws), left).unref());
    }
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
      const principal = principals.get(ws);
      if (!principal) continue;
      if (!visibleTo(principal, control, msg)) {
        // The deletion of a session a connection watched reaches it even though the session is gone.
        if (!(msg.type === 'session.deleted' && subscriptions.get(ws)?.has(msg.sessionId)))
          continue;
      }
      if (
        (msg.type === 'session.output' ||
          msg.type === 'session.listening' ||
          msg.type === 'session.rule' ||
          msg.type === 'session.rules.unenforced') &&
        !subscriptions.get(ws)?.has(msg.sessionId)
      ) {
        continue;
      }
      sendToBrowser(ws, payload);
      // A subscribed session may have lost or regained its owner: say what it is now.
      if (msg.type === 'device.presence') {
        for (const sessionId of subscriptions.get(ws) ?? []) sendRuntime(ws, sessionId);
      }
    }
  });

  setInterval(() => {
    for (const [ws, principal] of [...principals]) {
      if (principal.kind === 'token' && !options.tokenActive(principal.token.id)) endToken(ws);
    }
  }, TOKEN_CHECK_MS).unref();

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
