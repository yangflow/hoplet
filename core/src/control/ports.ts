import {
  PORTS_WATCH_MAX_SESSIONS,
  PROTOCOL_VERSION,
  type SessionPort,
  type SessionPortsResult,
} from '@hoplet/shared';
import { type ControlState, onlineLink } from './state.ts';
import type { DeviceLink } from './types.ts';

/**
 * The dev server links of the sessions browsers have open (ADR 0026). The core tells each agent which of its
 * sessions to scan, keeps the latest report of each in memory and passes it on to the browsers subscribed to the
 * session. Nothing is stored: after a restart the agents report again once a browser subscribes.
 */
export function createPorts(
  state: ControlState,
  open: ReadonlyMap<string, number>,
  ownerOf: (sessionId: string) => string | null,
) {
  /** The set last sent to each device, as a sorted, comma-joined key. */
  const sent = new Map<string, string>();

  function deviceOf(link: DeviceLink): string | null {
    for (const [deviceId, attached] of state.byDevice) if (attached === link) return deviceId;
    return null;
  }

  function wanted(deviceId: string): string[] {
    const ids: string[] = [];
    for (const sessionId of open.keys()) if (ownerOf(sessionId) === deviceId) ids.push(sessionId);
    return ids.sort().slice(0, PORTS_WATCH_MAX_SESSIONS);
  }

  /** Sends the device the sessions to scan when they differ from what it was last told. */
  function sync(deviceId: string): void {
    const link = onlineLink(state, deviceId);
    if (!link) return;
    const sessionIds = wanted(deviceId);
    const key = sessionIds.join(',');
    if (key === (sent.get(deviceId) ?? '')) return;
    sent.set(deviceId, key);
    link.send({ v: PROTOCOL_VERSION, type: 'ports.watch', sessionIds });
  }

  function of(sessionId: string): SessionPortsResult {
    const entry = state.sessionPorts.get(sessionId);
    const deviceId = entry?.deviceId ?? ownerOf(sessionId);
    const previewHost = deviceId ? state.deviceInfo.get(deviceId)?.previewHost : undefined;
    return {
      ...(previewHost !== undefined ? { previewHost } : {}),
      ports: entry?.ports ?? [],
    };
  }

  function publish(sessionId: string): void {
    state.publish({ v: PROTOCOL_VERSION, type: 'session.listening', sessionId, ...of(sessionId) });
  }

  return {
    of,
    /** Browsers opened or closed a session, or it changed owner: tell the agents what to scan. */
    sync(sessionId?: string): void {
      const owner = sessionId === undefined ? null : ownerOf(sessionId);
      if (owner) sync(owner);
      else for (const deviceId of [...state.byDevice.keys()]) sync(deviceId);
    },
    /** A new agent connection knows nothing: it is told again. */
    attached(deviceId: string): void {
      sent.delete(deviceId);
      sync(deviceId);
    },
    /** The device is gone: its reports are stale, browsers are told there are no ports. */
    detached(deviceIds: readonly string[]): void {
      for (const deviceId of deviceIds) {
        sent.delete(deviceId);
        for (const [sessionId, entry] of [...state.sessionPorts]) {
          if (entry.deviceId !== deviceId) continue;
          state.sessionPorts.delete(sessionId);
          publish(sessionId);
        }
      }
    },
    receive(link: DeviceLink, sessionId: string, ports: SessionPort[]): void {
      const deviceId = deviceOf(link);
      if (!deviceId || ownerOf(sessionId) !== deviceId) return;
      const before = state.sessionPorts.get(sessionId)?.ports ?? [];
      if (JSON.stringify(before) === JSON.stringify(ports)) return;
      if (ports.length === 0) state.sessionPorts.delete(sessionId);
      else state.sessionPorts.set(sessionId, { deviceId, ports });
      publish(sessionId);
    },
  };
}
