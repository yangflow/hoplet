import type { DeviceInfo, DeviceSystem, LiveSessionMeta, ProviderInfo } from '@hoplet/shared';
import { t } from '../../lib/i18n.ts';
import type { SessionTarget } from './model.ts';

export type DirGroup = { path: string; name: string; sessions: LiveSessionMeta[] };
export type DeviceGroup = {
  name: string;
  deviceId: string | null;
  online: boolean;
  /** What the agent announced about its machine; null for an agent that sent none. */
  system: DeviceSystem | null;
  providers: ProviderInfo[];
  dirs: DirGroup[];
};

/** The name Hoplet shows for a session: the user's own title, else the vendor's, else a placeholder. */
export function displayTitle(s: Pick<LiveSessionMeta, 'title' | 'titleOverride'>): string {
  return s.titleOverride?.trim() || s.title || t('Untitled session');
}

export const isPinned = (s: LiveSessionMeta): boolean => s.pinnedAt !== undefined;
export const isArchived = (s: LiveSessionMeta): boolean => s.archivedAt !== undefined;

/** The directory of a session: its project path, or the path its dashed store name stands for. */
export function sessionPath(s: LiveSessionMeta): string {
  const raw = s.projectPath.trim() || s.projectDir.replace(/^-/, '/').replace(/-/g, '/');
  return raw.replace(/[/\\]+$/, '') || raw;
}

/** The last segment of a path: the directory name the tree shows. */
export function pathLeaf(path: string): string {
  const parts = path.split(/[/\\]+/).filter(Boolean);
  return parts[parts.length - 1] ?? path;
}

/** Whether the agent of a host is connected: from the device list, or from a session's live owner. */
export function deviceConnected(
  name: string,
  group: LiveSessionMeta[],
  devices: DeviceInfo[],
): boolean {
  const ids = new Set(group.map((s) => s.ownerDeviceId).filter((id): id is string => !!id));
  if (devices.some((d) => d.connected && (d.name === name || ids.has(d.deviceId)))) return true;
  return group.some((s) => s.ownerConnected);
}

/** The device record behind a host name, preferring a connected one. */
export function liveDevice(
  name: string,
  group: LiveSessionMeta[],
  devices: DeviceInfo[],
): DeviceInfo | null {
  const ids = new Set(group.map((s) => s.ownerDeviceId).filter((id): id is string => !!id));
  const match = (d: DeviceInfo) => d.name === name || ids.has(d.deviceId);
  const hit = devices.find((d) => d.connected && match(d)) ?? devices.find(match);
  if (hit) return hit;
  const ownerId = group.find((s) => s.ownerDeviceId)?.ownerDeviceId;
  if (ownerId)
    return {
      deviceId: ownerId,
      name,
      connected: group.some((s) => s.ownerConnected),
      system: null,
      providers: [],
    };
  return null;
}

/** device > directory > session. Devices by name, whatever their presence; newest session first. */
export function buildTree(
  sessions: LiveSessionMeta[],
  devices: DeviceInfo[],
  /** The watched project paths per device ID: one with no session still gets its row. */
  watched: Readonly<Record<string, readonly string[]>> = {},
): DeviceGroup[] {
  const byDevice = new Map<string, LiveSessionMeta[]>();
  for (const s of sessions) {
    const device = s.deviceName.trim() || t('Unknown device');
    const list = byDevice.get(device) ?? [];
    list.push(s);
    byDevice.set(device, list);
  }
  for (const d of devices) {
    if (d.connected && !byDevice.has(d.name)) byDevice.set(d.name, []);
  }
  const groups = [...byDevice.entries()].map(([name, list]): DeviceGroup => {
    const dirs = new Map<string, DirGroup>();
    for (const s of list) {
      const path = sessionPath(s);
      const dir = dirs.get(path) ?? { path, name: pathLeaf(path), sessions: [] };
      dir.sessions.push(s);
      dirs.set(path, dir);
    }
    const device = liveDevice(name, list, devices);
    for (const raw of (device && watched[device.deviceId]) || []) {
      const path = raw.replace(/[/\\]+$/, '') || raw;
      if (!dirs.has(path)) dirs.set(path, { path, name: pathLeaf(path), sessions: [] });
    }
    const dirList = [...dirs.values()].sort((a, b) => a.name.localeCompare(b.name));
    for (const dir of dirList) dir.sessions.sort((a, b) => b.updatedAt - a.updatedAt);
    return {
      name,
      deviceId: device?.deviceId ?? null,
      online: deviceConnected(name, list, devices),
      system: device?.system ?? null,
      providers: device?.providers ?? [],
      dirs: dirList,
    };
  });
  return groups.sort((a, b) => a.name.localeCompare(b.name));
}

/** The places New session can start from, one per machine. */
export function sessionTargets(groups: DeviceGroup[]): SessionTarget[] {
  return groups.map((g) => ({
    deviceName: g.name,
    deviceId: g.deviceId,
    online: g.online,
    system: g.system,
    providers: g.providers,
    dirs: g.dirs.map((d) => ({ path: d.path, name: d.name })),
  }));
}
