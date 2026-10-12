import {
  type ErrorCode,
  PROTOCOL_VERSION,
  type ServerMessage,
  WIRE_MESSAGES,
  type WireType,
} from '@hoplet/shared';
import type { DeviceLink } from '../control/index.ts';

const OPEN = 1;

/** One authenticated agent connection. */
export interface Conn {
  ws: Outlet & { ping(): void };
  /** What control sees of this connection once the device has said hello. */
  link: DeviceLink;
  /** The device the token it connected with belongs to. */
  boundDeviceId: string;
  /** Set by `device.hello`, which must name the bound device. */
  deviceId: string | null;
  alive: boolean;
}

/** The part of a socket that sending needs. */
export interface Outlet {
  readyState: number;
  send(data: string, cb?: (err?: Error) => void): void;
  terminate(): void;
}

/** A bulk message waiting to be written; `done` hears how the write ended. */
interface Queued {
  payload: string;
  done?: (err?: Error) => void;
}

/**
 * Sends on the connection's two lanes (docs/architecture.md#principles). A control message is written
 * at once; a bulk message waits until the previous bulk message has left, so a control message is never
 * behind more than the one bulk message already being written.
 */
export function createLink(ws: Outlet): DeviceLink {
  const bulk: Queued[] = [];
  let writing = false;

  function drain(err: Error): void {
    for (const queued of bulk.splice(0)) queued.done?.(err);
  }

  function pump(): void {
    const next = bulk.shift();
    if (next === undefined) {
      writing = false;
      return;
    }
    if (ws.readyState !== OPEN) {
      writing = false;
      next.done?.(new Error('connection closed'));
      drain(new Error('connection closed'));
      return;
    }
    writing = true;
    ws.send(next.payload, (err) => {
      next.done?.(err);
      pump();
    });
  }

  function enqueue(msg: ServerMessage, done?: (err?: Error) => void): void {
    const payload = JSON.stringify(msg);
    const lane = WIRE_MESSAGES[msg.type as WireType]?.lane ?? 'control';
    if (ws.readyState !== OPEN) {
      done?.(new Error('connection closed'));
      return;
    }
    if (lane === 'control') {
      ws.send(payload, done);
      return;
    }
    bulk.push(done ? { payload, done } : { payload });
    if (!writing) pump();
  }

  return {
    send(msg) {
      enqueue(msg);
    },
    sendAndWait(msg) {
      return new Promise<void>((resolve, reject) => {
        enqueue(msg, (err) => (err ? reject(err) : resolve()));
      });
    },
    isOpen: () => ws.readyState === OPEN,
  };
}

export function newConn(ws: Conn['ws'], boundDeviceId: string): Conn {
  return { ws, link: createLink(ws), boundDeviceId, deviceId: null, alive: true };
}

export function send(conn: Conn, msg: ServerMessage): void {
  conn.link.send(msg);
}

export function sendError(conn: Conn, code: ErrorCode, message: string): void {
  send(conn, { v: PROTOCOL_VERSION, type: 'error', code, message });
}
