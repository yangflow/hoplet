import type { LiveSessionMeta } from '@hoplet/shared';
import { useCallback, useState } from 'react';
import { type DeviceGroup, isPinned, pathLeaf } from '../group.ts';

/**
 * How the sidebar groups the sessions (docs/spec.md#organizing-sessions): by machine (each machine's own view), by
 * project across the machines that share a git remote (ADR 0041), or by last activity. Remembered in the browser,
 * never sent to the core.
 */
export type Grouping = 'machine' | 'project' | 'time';
export const GROUPINGS: readonly Grouping[] = ['machine', 'project', 'time'];
export const GROUPING_KEY = 'hoplet.grouping';

type Store = Pick<Storage, 'getItem' | 'setItem'>;

function storage(): Store | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage;
  } catch {
    return null;
  }
}

export function loadGrouping(store: Store | null = storage()): Grouping {
  try {
    const raw = store?.getItem(GROUPING_KEY);
    return GROUPINGS.find((g) => g === raw) ?? 'machine';
  } catch {
    return 'machine';
  }
}

/** The grouping and a setter that saves it; `initial` replaces what is saved (development fixtures). */
export function useGrouping(initial?: Grouping): [Grouping, (g: Grouping) => void] {
  const [grouping, set] = useState<Grouping>(() => initial ?? loadGrouping());
  const choose = useCallback((g: Grouping) => {
    set(g);
    try {
      storage()?.setItem(GROUPING_KEY, g);
    } catch {
      /* a browser that refuses storage forgets the choice on reload */
    }
  }, []);
  return [grouping, choose];
}

/** The tags in use and how many sessions carry each, most used first, then by name. */
export function tagCounts(sessions: readonly LiveSessionMeta[]): { tag: string; count: number }[] {
  const counts = new Map<string, number>();
  for (const s of sessions)
    for (const tag of s.tags ?? []) counts.set(tag, (counts.get(tag) ?? 0) + 1);
  return [...counts]
    .map(([tag, count]) => ({ tag, count }))
    .sort((a, b) => b.count - a.count || a.tag.localeCompare(b.tag));
}

export const hasTag = (s: LiveSessionMeta, tag: string): boolean => (s.tags ?? []).includes(tag);

/** The machines and folders that hold a session with the tag, with only those sessions. */
export function filterByTag(groups: readonly DeviceGroup[], tag: string | null): DeviceGroup[] {
  if (tag === null) return [...groups];
  return groups
    .map((g) => ({
      ...g,
      dirs: g.dirs
        .map((d) => ({ ...d, sessions: d.sessions.filter((s) => hasTag(s, tag)) }))
        .filter((d) => d.sessions.length > 0),
    }))
    .filter((g) => g.dirs.length > 0);
}

/** A session with where it lives: its machine, whether that machine is online, and its folder's name. */
export type Located = {
  session: LiveSessionMeta;
  device: string;
  online: boolean;
  dir: string;
};

function located(groups: readonly DeviceGroup[]): Located[] {
  return groups.flatMap((g) =>
    g.dirs.flatMap((d) =>
      d.sessions.map((session) => ({ session, device: g.name, online: g.online, dir: d.name })),
    ),
  );
}

const newestFirst = (a: Located, b: Located) => b.session.updatedAt - a.session.updatedAt;

/** The pinned sessions of every machine, newest first: they lead the project and time groupings. */
export function pinnedAcross(groups: readonly DeviceGroup[]): Located[] {
  return located(groups)
    .filter((l) => isPinned(l.session))
    .sort(newestFirst);
}

/**
 * One project: the sessions of every machine whose folder has this git remote (ADR 0041), or one machine's folder
 * when it has none. `machines` counts the machines it joins.
 */
export type ProjectGroup = {
  key: string;
  name: string;
  /** The normalized remote; absent for a folder without one. */
  repository?: string;
  machines: number;
  sessions: Located[];
};

/** The unpinned sessions by project, the project with the latest activity first. */
export function projectGroups(groups: readonly DeviceGroup[]): ProjectGroup[] {
  const byKey = new Map<string, ProjectGroup>();
  for (const l of located(groups)) {
    if (isPinned(l.session)) continue;
    const repository = l.session.repository;
    const key = repository ? `repo:${repository}` : `dir:${l.device}\n${l.dir}`;
    const group = byKey.get(key) ?? {
      key,
      name: repository ? pathLeaf(repository) : l.dir,
      ...(repository ? { repository } : {}),
      machines: 0,
      sessions: [],
    };
    group.sessions.push(l);
    byKey.set(key, group);
  }
  const out = [...byKey.values()];
  for (const g of out) {
    g.sessions.sort(newestFirst);
    g.machines = new Set(g.sessions.map((l) => l.device)).size;
  }
  const latest = (g: ProjectGroup) => g.sessions[0]?.session.updatedAt ?? 0;
  return out.sort((a, b) => latest(b) - latest(a) || a.name.localeCompare(b.name));
}

export type Bucket = 'today' | 'yesterday' | 'week' | 'earlier';
export const BUCKETS: readonly Bucket[] = ['today', 'yesterday', 'week', 'earlier'];

/** Which day bucket a moment falls in, by the calendar days of this browser before `now`. */
export function bucketOf(at: number, now: number): Bucket {
  const day = (ms: number) => {
    const d = new Date(ms);
    return Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()) / 86_400_000;
  };
  const ago = day(now) - day(at);
  if (ago <= 0) return 'today';
  if (ago === 1) return 'yesterday';
  if (ago <= 6) return 'week';
  return 'earlier';
}

/** The unpinned sessions by last activity, newest first; empty buckets are left out. */
export function timeGroups(
  groups: readonly DeviceGroup[],
  now: number,
): { bucket: Bucket; sessions: Located[] }[] {
  const rest = located(groups)
    .filter((l) => !isPinned(l.session))
    .sort(newestFirst);
  return BUCKETS.map((bucket) => ({
    bucket,
    sessions: rest.filter((l) => bucketOf(l.session.updatedAt, now) === bucket),
  })).filter((b) => b.sessions.length > 0);
}
