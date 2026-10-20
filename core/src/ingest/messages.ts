import { type ClientMessage, PROTOCOL_VERSION, type SessionRejected } from '@hoplet/shared';
import type { AgentControl } from '../control/index.ts';
import { log } from '../log/index.ts';
import { NotOwnerError, SnapshotError, type Store } from '../store/index.ts';
import type { Conn } from './conn.ts';
import { send, sendError } from './conn.ts';
import { decodeLines } from './decode.ts';

export type MessageDeps = {
  store: Store;
  control: AgentControl;
};

export function handleMessage(deps: MessageDeps, conn: Conn, msg: ClientMessage): void {
  const { store, control } = deps;

  switch (msg.type) {
    case 'device.hello': {
      if (msg.deviceId !== conn.boundDeviceId) {
        sendError(conn, 'device-mismatch', 'hello deviceId must match the device token');
        return;
      }
      conn.deviceId = msg.deviceId;
      control.attach(msg.deviceId, conn.link, msg.providers, {
        capabilities: msg.capabilities,
        previewHost: msg.previewHost,
      });
      store.upsertDevice(msg.deviceId, msg.name, msg.version, msg.system);
      log('info', 'ingest', 'device online', {
        device: msg.deviceId,
        name: msg.name,
        version: msg.version,
      });
      return;
    }
    case 'ping': {
      send(conn, { v: PROTOCOL_VERSION, type: 'pong', t: msg.t });
      return;
    }
    case 'session.append': {
      if (!conn.deviceId) {
        sendError(conn, 'hello-first', 'send device.hello first');
        return;
      }
      // A deleted session is not recreated by a sync (ADR 0023).
      if (store.isTombstoned(msg.sessionId)) {
        send(conn, {
          v: PROTOCOL_VERSION,
          type: 'session.rejected',
          sessionId: msg.sessionId,
          reason: 'deleted',
        });
        return;
      }
      try {
        const session = store.getSession(msg.sessionId);
        if (session?.ownerDeviceId && session.ownerDeviceId !== conn.deviceId) {
          throw new NotOwnerError(session.ownerDeviceId);
        }
        const byteSize = session?.byteSize ?? 0;
        if (msg.byteStart !== byteSize) {
          send(conn, {
            v: PROTOCOL_VERSION,
            type: 'session.resync',
            sessionId: msg.sessionId,
            byteSize,
          });
          return;
        }
        // No await between checking the cursor and the synchronous store append.
        const lines = decodeLines(msg);
        const r = store.append(
          {
            sessionId: msg.sessionId,
            projectDir: msg.projectDir,
            filePath: msg.filePath,
            deviceId: conn.deviceId,
            provider: msg.provider,
            ...(msg.title !== undefined ? { title: msg.title } : {}),
            ...(msg.configuration !== undefined ? { configuration: msg.configuration } : {}),
          },
          lines,
        );
        send(conn, {
          v: PROTOCOL_VERSION,
          type: 'session.ack',
          sessionId: msg.sessionId,
          byteSize: r.byteSize,
          lineCount: r.lineCount,
        });
        if (r.appendedLines > 0) {
          control.publish({
            v: PROTOCOL_VERSION,
            type: 'session.appended',
            sessionId: msg.sessionId,
            appendedLines: r.appendedLines,
            totalLines: r.lineCount,
            byteSize: r.byteSize,
            updatedAt: Date.now(),
            isNew: r.isNew,
          });
          log(
            r.isNew ? 'info' : 'debug',
            'ingest',
            `${r.isNew ? 'new session' : 'appended'} ${msg.sessionId.slice(0, 8)}`,
            { lines: r.appendedLines, bytes: lines.join('\n').length, total: r.lineCount },
          );
        }
      } catch (err) {
        if (err instanceof NotOwnerError) {
          const rej: SessionRejected = {
            v: PROTOCOL_VERSION,
            type: 'session.rejected',
            sessionId: msg.sessionId,
            reason: 'not-owner',
            ownerDeviceId: err.ownerDeviceId,
          };
          send(conn, rej);
          return;
        }
        throw err;
      }
      return;
    }
    case 'workspace.snapshot': {
      if (!conn.deviceId) {
        sendError(conn, 'hello-first', 'send device.hello first');
        return;
      }
      const session = store.getSession(msg.sessionId);
      if (!session || session.ownerDeviceId !== conn.deviceId) {
        sendError(conn, 'snapshot-rejected', 'the device does not own the session');
        return;
      }
      try {
        const stored = store.putSnapshotChunk(msg);
        if (stored) {
          log('debug', 'ingest', `snapshot stored for ${msg.sessionId.slice(0, 8)}`, {
            bytes: stored.byteSize,
          });
        }
      } catch (err) {
        if (!(err instanceof SnapshotError)) throw err;
        log('warn', 'ingest', 'snapshot refused', { reason: err.reason });
        sendError(conn, 'snapshot-rejected', err.reason);
      }
      return;
    }
    default:
      control.receive(conn.link, msg);
      return;
  }
}
