import type { ServerMessage } from '@hoplet/shared';
import { PROTOCOL_VERSION } from '@hoplet/shared';

/**
 * What the browser WebSocket tells the screens: every server message, and the link going up or down.
 * The socket itself lives in App; screens listen here and subscribe to the sessions they show.
 */
export type LiveEvent =
  | { kind: 'message'; msg: ServerMessage }
  | { kind: 'open' }
  | { kind: 'close' };

type Listener = (event: LiveEvent) => void;
type Sender = (payload: string) => void;

const listeners = new Set<Listener>();
const subscriptions = new Map<string, number>();
let sender: Sender | null = null;

export function onLive(listener: Listener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function emitLive(event: LiveEvent): void {
  for (const listener of [...listeners]) listener(event);
}

function frame(type: 'subscribe' | 'unsubscribe', sessionId: string): string {
  return JSON.stringify({ v: PROTOCOL_VERSION, type, sessionId });
}

/** App hands over the open socket's send function, or null while there is none; open sockets get every subscription. */
export function setLiveSender(next: Sender | null): void {
  sender = next;
  if (!next) return;
  for (const sessionId of subscriptions.keys()) next(frame('subscribe', sessionId));
}

/** Sends one browser message on the open socket; false when there is no open socket. */
export function sendLive(message: { type: string; [key: string]: unknown }): boolean {
  if (!sender) return false;
  sender(JSON.stringify({ v: PROTOCOL_VERSION, ...message }));
  return true;
}

/** Subscribes this window to a session's streamed output; the subscription survives reconnects. */
export function subscribeSession(sessionId: string): () => void {
  subscriptions.set(sessionId, (subscriptions.get(sessionId) ?? 0) + 1);
  sender?.(frame('subscribe', sessionId));
  return () => {
    const count = (subscriptions.get(sessionId) ?? 1) - 1;
    if (count > 0) {
      subscriptions.set(sessionId, count);
      return;
    }
    subscriptions.delete(sessionId);
    sender?.(frame('unsubscribe', sessionId));
  };
}
