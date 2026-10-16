/** Agent WebSocket `/ws/agent`: device-token authentication, welcome, session.append, ack, owner check. */
import type { IncomingMessage, Server } from 'node:http';
import { PROTOCOL_VERSION } from '@hoplet/shared';
import { type WebSocket, WebSocketServer } from 'ws';
import { bearerToken } from '../config/index.ts';
import type { AgentControl } from '../control/index.ts';
import { log, registerSecret } from '../log/index.ts';
import type { Store } from '../store/index.ts';
import { type Conn, newConn, send, sendError } from './conn.ts';
import { parseFrame } from './decode.ts';
import { handleMessage } from './messages.ts';

export function attachIngest(server: Server, store: Store, control: AgentControl): void {
  const conns = new Set<Conn>();
  const wss = new WebSocketServer({ noServer: true, maxPayload: 64 * 1024 * 1024 });
  const messageDeps = { store, control };

  function drop(conn: Conn): void {
    conns.delete(conn);
    control.detach(conn.link);
  }

  server.on('upgrade', (req, socket, head) => {
    const url = new URL(req.url ?? '/', `http://${req.headers.host ?? 'localhost'}`);
    if (url.pathname !== '/ws/agent') return;
    // Only a device token, only in the header: the host token and any token in the URL are refused.
    const token = bearerToken(req.headers.authorization);
    const deviceId = store.deviceIdForToken(token);
    if (!deviceId) {
      socket.write('HTTP/1.1 401 Unauthorized\r\nconnection: close\r\n\r\n');
      socket.destroy();
      return;
    }
    registerSecret(token);
    wss.handleUpgrade(req, socket, head, (ws) => wss.emit('connection', ws, req, deviceId));
  });

  wss.on('connection', (ws: WebSocket, _req: IncomingMessage, boundDeviceId: string) => {
    const conn = newConn(ws, boundDeviceId);
    conns.add(conn);
    log('info', 'ingest', 'connection opened', { connections: conns.size });

    send(conn, {
      v: PROTOCOL_VERSION,
      type: 'welcome',
      serverTime: Date.now(),
      sessions: store.listSessions(5000),
    });

    ws.on('message', (data, isBinary) => {
      if (isBinary) return;
      const frame = parseFrame(data.toString());
      if (!frame.ok) {
        sendError(conn, frame.code, frame.message);
        if (frame.close) {
          log('warn', 'ingest', 'agent refused', { code: frame.code, message: frame.message });
          ws.close(1008, frame.code);
        }
        return;
      }
      const msg = frame.msg;
      const handle = () => {
        try {
          handleMessage(messageDeps, conn, msg);
        } catch (err) {
          log('error', 'ingest', 'message handler failed', { error: String(err) });
          sendError(conn, 'internal', String(err));
        }
      };
      if (msg.type !== 'session.append' && msg.type !== 'workspace.snapshot') {
        handle();
        return;
      }
      // Sync appends and snapshot chunks are bulk: they wait their turn in the bulk queue so control
      // messages and requests that arrive meanwhile are handled first. The queue starts tasks in the order
      // they were queued and a handler runs to its end in one turn, so the chunks of a snapshot stay in
      // order. A full queue answers a refused append with the cursor, and the agent sends again from there;
      // a refused snapshot chunk breaks its upload, and the next snapshot replaces it.
      control.bulk.run(handle).catch(() => {
        if (msg.type === 'workspace.snapshot') {
          sendError(conn, 'busy', 'snapshot chunk dropped');
          return;
        }
        if (store.isTombstoned(msg.sessionId)) {
          send(conn, {
            v: PROTOCOL_VERSION,
            type: 'session.rejected',
            sessionId: msg.sessionId,
            reason: 'deleted',
          });
          return;
        }
        send(conn, {
          v: PROTOCOL_VERSION,
          type: 'session.resync',
          sessionId: msg.sessionId,
          byteSize: store.getSession(msg.sessionId)?.byteSize ?? 0,
        });
      });
    });

    ws.on('pong', () => {
      conn.alive = true;
    });
    ws.on('close', () => {
      drop(conn);
    });
    ws.on('error', (err) => {
      log('warn', 'ingest', 'connection error', { error: err.message });
      drop(conn);
    });
  });

  setInterval(() => {
    for (const c of conns) {
      if (!c.alive) {
        c.ws.terminate();
        drop(c);
        continue;
      }
      c.alive = false;
      c.ws.ping();
    }
  }, 30_000).unref();
}
