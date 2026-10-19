import {
  type DeviceInfo,
  type DeviceResourceSample,
  type DeviceResources,
  RESOURCES_DISK_WARN,
  RESOURCES_MEMORY_WARN,
  RESOURCES_RING_SIZE,
  RESOURCES_SESSIONS_MAX,
} from '@hoplet/shared';
import type { ControlState, ResourcesEntry } from './state.ts';
import type { DeviceLink, ResourcesSnapshot } from './types.ts';

/** Whether a reading shows the memory or the disk short. A volume or memory of size 0 warns about nothing. */
export function resourceWarnings(sample: DeviceResourceSample): { memory: boolean; disk: boolean } {
  return {
    memory: sample.memTotal > 0 && sample.memUsed / sample.memTotal > RESOURCES_MEMORY_WARN,
    disk: sample.diskTotal > 0 && sample.diskFree / sample.diskTotal < RESOURCES_DISK_WARN,
  };
}

/**
 * The CPU, memory and disk of each machine and the usage of its sessions' processes. The core keeps the last
 * hour of readings of each machine, and of each of its sessions, in memory: they survive the agent disconnecting, so an offline machine shows
 * its last values and when they were taken, and are lost when the core restarts. Nothing is stored.
 */
export function createResources(state: ControlState) {
  function deviceOf(link: DeviceLink): string | null {
    for (const [deviceId, attached] of state.byDevice) if (attached === link) return deviceId;
    return null;
  }

  return {
    of(deviceId: string): ResourcesSnapshot {
      const entry = state.resources.get(deviceId);
      return {
        samples: entry ? [...entry.samples] : [],
        sessions: entry ? [...entry.sessions] : [],
        history: new Map(entry ? [...entry.history].map(([id, pts]) => [id, [...pts]]) : []),
      };
    },
    /** The latest reading and its warnings for the device list; the warnings only while the device is connected. */
    extras(deviceId: string, connected: boolean): Pick<DeviceInfo, 'resources' | 'warn'> {
      const latest = state.resources.get(deviceId)?.samples.at(-1);
      if (!latest) return {};
      const warn = connected ? resourceWarnings(latest) : { memory: false, disk: false };
      return { resources: latest, warn };
    },
    receive(link: DeviceLink, msg: DeviceResources): void {
      const deviceId = deviceOf(link);
      if (!deviceId) return;
      const entry: ResourcesEntry = state.resources.get(deviceId) ?? {
        samples: [],
        sessions: [],
        history: new Map(),
      };
      entry.samples.push({
        at: msg.at,
        cpu: msg.cpu,
        memUsed: msg.memUsed,
        memTotal: msg.memTotal,
        diskFree: msg.diskFree,
        diskTotal: msg.diskTotal,
      });
      if (entry.samples.length > RESOURCES_RING_SIZE) {
        entry.samples.splice(0, entry.samples.length - RESOURCES_RING_SIZE);
      }
      entry.sessions = msg.sessions;
      for (const s of msg.sessions) {
        const points = entry.history.get(s.sessionId) ?? [];
        points.push({ at: msg.at, cpu: s.cpu, rss: s.rss });
        if (points.length > RESOURCES_RING_SIZE)
          points.splice(0, points.length - RESOURCES_RING_SIZE);
        entry.history.set(s.sessionId, points);
      }
      // A point older than the oldest machine reading kept is out of the window; a session with none left is gone.
      const oldest = entry.samples[0]?.at ?? msg.at;
      for (const [id, points] of entry.history) {
        const keep = points.findIndex((pt) => pt.at >= oldest);
        if (keep < 0) entry.history.delete(id);
        else if (keep > 0) points.splice(0, keep);
      }
      // Bounded to one report's worth of sessions: the ones seen least recently go first.
      if (entry.history.size > RESOURCES_SESSIONS_MAX) {
        const byRecency = [...entry.history].sort(
          (a, b) => (b[1].at(-1)?.at ?? 0) - (a[1].at(-1)?.at ?? 0),
        );
        for (const [id] of byRecency.slice(RESOURCES_SESSIONS_MAX)) entry.history.delete(id);
      }
      state.resources.set(deviceId, entry);
    },
  };
}
