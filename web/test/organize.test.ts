import type { DeviceInfo, LiveSessionMeta } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import { buildTree } from '../src/screens/cockpit/group.ts';
import {
  bucketOf,
  filterByTag,
  GROUPING_KEY,
  loadGrouping,
  pinnedAcross,
  projectGroups,
  tagCounts,
  timeGroups,
} from '../src/screens/cockpit/sidebar/organize.ts';
import { normalTag, tagOptions } from '../src/screens/cockpit/tag-editor.tsx';

const meta = (id: string, over: Partial<LiveSessionMeta> = {}): LiveSessionMeta =>
  ({
    sessionId: id,
    title: `vendor ${id}`,
    deviceName: 'mac',
    ownerDeviceId: 'dev-mac',
    projectPath: '/w/app',
    projectDir: '-w-app',
    provider: 'claude',
    ownerConnected: true,
    createdAt: 1,
    updatedAt: 1,
    lineCount: 1,
    byteSize: 1,
    ownerEpoch: 0,
    ...over,
  }) as LiveSessionMeta;
const devices: DeviceInfo[] = [
  { deviceId: 'dev-mac', name: 'mac', connected: true },
  { deviceId: 'dev-box', name: 'box', connected: false },
];
const onBox = { deviceName: 'box', ownerDeviceId: 'dev-box' };

describe('tags', () => {
  it('counts the tags in use, most used first, then by name', () => {
    const list = [
      meta('a', { tags: ['sync', 'bug'] }),
      meta('b', { tags: ['sync'] }),
      meta('c', { tags: ['docs'] }),
      meta('d'),
    ];
    expect(tagCounts(list)).toEqual([
      { tag: 'sync', count: 2 },
      { tag: 'bug', count: 1 },
      { tag: 'docs', count: 1 },
    ]);
  });

  it('keeps only the machines and folders that hold a session with the tag', () => {
    const tree = buildTree(
      [
        meta('a', { tags: ['sync'] }),
        meta('b', { projectPath: '/w/other', projectDir: '-w-other' }),
        meta('c', { ...onBox, tags: ['docs'] }),
      ],
      devices,
      {},
    );
    const sync = filterByTag(tree, 'sync');
    expect(sync.map((g) => g.name)).toEqual(['mac']);
    expect(sync[0]?.dirs.map((d) => d.sessions.map((s) => s.sessionId))).toEqual([['a']]);
    expect(filterByTag(tree, null)).toEqual(tree);
    expect(filterByTag(tree, 'none')).toEqual([]);
  });

  it('normalizes what is typed and refuses what is not a tag', () => {
    expect(normalTag('  Release-2 ')).toBe('release-2');
    expect(normalTag('two words')).toBeNull();
    expect(normalTag('')).toBeNull();
    expect(normalTag('x'.repeat(33))).toBeNull();
  });

  it('offers the matching tags the session lacks, then a new tag', () => {
    const existing = [
      { tag: 'release', count: 3 },
      { tag: 'refactor', count: 1 },
      { tag: 'sync', count: 2 },
    ];
    expect(tagOptions('re', ['refactor'], existing)).toEqual([
      { tag: 'release', count: 3 },
      { tag: 're', create: true },
    ]);
    expect(tagOptions('Release', [], existing)).toEqual([{ tag: 'release', count: 3 }]);
    expect(tagOptions('bad tag', [], existing)).toEqual([]);
  });
});

describe('grouping', () => {
  it('reads the saved grouping and falls back to machine', () => {
    const store = (value: string | null) => ({ getItem: () => value, setItem: () => undefined });
    expect(loadGrouping(store('project'))).toBe('project');
    expect(loadGrouping(store('time'))).toBe('time');
    expect(loadGrouping(store('nonsense'))).toBe('machine');
    expect(loadGrouping(store(null))).toBe('machine');
    expect(loadGrouping(null)).toBe('machine');
    expect(GROUPING_KEY).toBe('hoplet.grouping');
  });

  it('joins the machines that share a remote and keeps a folder without one apart', () => {
    const repo = 'github.com/acme/app';
    const tree = buildTree(
      [
        meta('a', { repository: repo, updatedAt: 10 }),
        meta('b', { ...onBox, projectPath: '/src/app', repository: repo, updatedAt: 30 }),
        meta('c', { projectPath: '/w/notes', projectDir: '-w-notes', updatedAt: 20 }),
        meta('d', { ...onBox, projectPath: '/w/notes', updatedAt: 5 }),
        meta('e', { repository: repo, pinnedAt: 1, updatedAt: 40 }),
      ],
      devices,
      {},
    );
    const projects = projectGroups(tree);
    expect(projects.map((p) => [p.name, p.repository, p.machines])).toEqual([
      ['app', repo, 2],
      ['notes', undefined, 1],
      ['notes', undefined, 1],
    ]);
    expect(projects[0]?.sessions.map((l) => l.session.sessionId)).toEqual(['b', 'a']);
    expect(projects[0]?.sessions.map((l) => l.device)).toEqual(['box', 'mac']);
    expect(pinnedAcross(tree).map((l) => l.session.sessionId)).toEqual(['e']);
  });

  it('puts a moment in the calendar day bucket of this browser', () => {
    const now = new Date(2026, 9, 8, 9, 0).getTime();
    expect(bucketOf(new Date(2026, 9, 8, 0, 5).getTime(), now)).toBe('today');
    expect(bucketOf(new Date(2026, 9, 7, 23, 55).getTime(), now)).toBe('yesterday');
    expect(bucketOf(new Date(2026, 9, 2, 12, 0).getTime(), now)).toBe('week');
    expect(bucketOf(new Date(2026, 9, 1, 12, 0).getTime(), now)).toBe('earlier');
  });

  it('lists the unpinned sessions by last activity and leaves empty buckets out', () => {
    const now = new Date(2026, 9, 8, 12, 0).getTime();
    const tree = buildTree(
      [
        meta('a', { updatedAt: now - 60_000 }),
        meta('b', { ...onBox, updatedAt: now - 30 * 86_400_000 }),
        meta('c', { updatedAt: now - 2 * 60_000 }),
        meta('d', { updatedAt: now, pinnedAt: 1 }),
      ],
      devices,
      {},
    );
    expect(
      timeGroups(tree, now).map((b) => [b.bucket, b.sessions.map((l) => l.session.sessionId)]),
    ).toEqual([
      ['today', ['a', 'c']],
      ['earlier', ['b']],
    ]);
  });
});
