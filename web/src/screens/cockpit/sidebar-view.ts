import type { LiveSessionMeta } from '@hoplet/shared';
import { useCallback, useState } from 'react';
import type { DeviceGroup } from './group.ts';

/**
 * How one machine lists its sessions (docs/spec.md#hoplet): remembered in the browser per device, never sent to
 * the core. `folder` lists the directories with their sessions, `flat` the machine's sessions as one list.
 */
export type Organize = 'folder' | 'flat';
export type Sort = 'activity' | 'created' | 'title';
export type MachineView = { organize: Organize; sort: Sort };
export type MachineViews = Readonly<Record<string, MachineView>>;

export const VIEWS_KEY = 'hoplet.machine-views';
export const DEFAULT_VIEW: MachineView = { organize: 'folder', sort: 'activity' };
export const ORGANIZE: readonly Organize[] = ['folder', 'flat'];
export const SORTS: readonly Sort[] = ['activity', 'created', 'title'];

/** The key a machine's view is saved under: its device ID, or its name while it has none. */
export function viewKey(group: Pick<DeviceGroup, 'deviceId' | 'name'>): string {
  return group.deviceId ?? `name:${group.name}`;
}

type Store = Pick<Storage, 'getItem' | 'setItem'>;

function storage(): Store | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}

function parseView(value: unknown): MachineView {
  const obj = (value && typeof value === 'object' ? value : {}) as Record<string, unknown>;
  return {
    organize: ORGANIZE.find((o) => o === obj.organize) ?? DEFAULT_VIEW.organize,
    sort: SORTS.find((s) => s === obj.sort) ?? DEFAULT_VIEW.sort,
  };
}

/** The saved views by machine; a machine with none, or a value that is not one of the known ones, gets the default. */
export function loadViews(store: Store | null = storage()): MachineViews {
  try {
    const raw = store?.getItem(VIEWS_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : null;
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed).map(([k, v]) => [k, parseView(v)]));
  } catch {
    return {};
  }
}

export function saveViews(views: MachineViews, store: Store | null = storage()): void {
  try {
    store?.setItem(VIEWS_KEY, JSON.stringify(views));
  } catch {
    /* a browser that refuses storage forgets the choice on reload */
  }
}

/** The views per machine and a setter that saves; `initial` replaces what is saved (development fixtures). */
export function useMachineViews(initial?: MachineViews): {
  viewOf: (key: string) => MachineView;
  setView: (key: string, patch: Partial<MachineView>) => void;
} {
  const [views, set] = useState<MachineViews>(() => initial ?? loadViews());
  const setView = useCallback((key: string, patch: Partial<MachineView>) => {
    set((current) => {
      const next = { ...current, [key]: { ...(current[key] ?? DEFAULT_VIEW), ...patch } };
      saveViews(next);
      return next;
    });
  }, []);
  return { viewOf: (key) => views[key] ?? DEFAULT_VIEW, setView };
}

/** A copy of the sessions in the order of the sort: newest first, or by title. */
export function sortSessions(list: readonly LiveSessionMeta[], sort: Sort): LiveSessionMeta[] {
  const out = [...list];
  if (sort === 'title') {
    return out.sort((a, b) => a.title.localeCompare(b.title) || b.updatedAt - a.updatedAt);
  }
  const key = (s: LiveSessionMeta) => (sort === 'created' ? s.createdAt : s.updatedAt);
  return out.sort((a, b) => key(b) - key(a));
}

/** A machine's directories with their sessions in the order of the sort. */
export function sortedDirs(group: DeviceGroup, sort: Sort): DeviceGroup['dirs'] {
  return group.dirs.map((d) => ({ ...d, sessions: sortSessions(d.sessions, sort) }));
}

/** A session of a machine with the name of its directory and its full path. */
export type Placed = { session: LiveSessionMeta; dir: string; path: string };

/** One machine's sessions as a single list in the order of the sort. */
export function flatList(group: DeviceGroup, sort: Sort): Placed[] {
  const placed = group.dirs.flatMap((d) =>
    d.sessions.map((session) => ({ session, dir: d.name, path: d.path })),
  );
  const rank = new Map(
    sortSessions(
      placed.map((p) => p.session),
      sort,
    ).map((s, i) => [s, i]),
  );
  return placed.sort((a, b) => (rank.get(a.session) ?? 0) - (rank.get(b.session) ?? 0));
}
