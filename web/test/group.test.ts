import type { DeviceInfo, LiveSessionMeta } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  buildTree,
  deviceConnected,
  liveDevice,
  pathLeaf,
  sessionPath,
  sessionTargets,
} from '../src/screens/cockpit/group.ts';
import { compactRel } from '../src/screens/cockpit/sidebar/rel.ts';

function session(
  over: Partial<LiveSessionMeta> & Pick<LiveSessionMeta, 'sessionId' | 'projectPath'>,
): LiveSessionMeta {
  return {
    projectDir: '',
    deviceName: 'MacBook Pro',
    title: over.sessionId,
    provider: 'claude',
    ownerConnected: false,
    createdAt: 1,
    updatedAt: 1,
    lineCount: 1,
    byteSize: 1,
    ownerEpoch: 0,
    ...over,
  };
}

const mac: DeviceInfo = { deviceId: 'dev-mac', name: 'MacBook Pro', connected: true };

describe('sessionPath and pathLeaf', () => {
  it('takes the project path and shows only its last directory', () => {
    const s = session({ sessionId: 'a', projectPath: '/Users/mark/github/repopro/hoplet/' });
    expect(sessionPath(s)).toBe('/Users/mark/github/repopro/hoplet');
    expect(pathLeaf(sessionPath(s))).toBe('hoplet');
  });

  it('falls back to the dashed store name', () => {
    const s = session({ sessionId: 'a', projectPath: '', projectDir: '-Users-mark-notes' });
    expect(sessionPath(s)).toBe('/Users/mark/notes');
  });
});

describe('buildTree', () => {
  it('groups device, directory, session with the newest session first', () => {
    const tree = buildTree(
      [
        session({ sessionId: '1', projectPath: '/a/hoplet', updatedAt: 1 }),
        session({ sessionId: '2', projectPath: '/a/hoplet', updatedAt: 5 }),
        session({ sessionId: '3', projectPath: '/a/app', updatedAt: 3 }),
      ],
      [mac],
    );
    expect(tree).toHaveLength(1);
    expect(tree[0]?.dirs.map((d) => d.name)).toEqual(['app', 'hoplet']);
    expect(tree[0]?.dirs[1]?.sessions.map((s) => s.sessionId)).toEqual(['2', '1']);
    expect(tree[0]?.deviceId).toBe('dev-mac');
  });

  it('keeps devices in name order whatever their presence and lists a connected one without sessions', () => {
    const tree = buildTree(
      [session({ sessionId: '1', projectPath: '/a/b', deviceName: 'office' })],
      [mac, { deviceId: 'dev-office', name: 'office', connected: false }],
    );
    expect(tree.map((g) => [g.name, g.online])).toEqual([
      ['MacBook Pro', true],
      ['office', false],
    ]);
    const offlineMac = buildTree(
      [
        session({ sessionId: '1', projectPath: '/a/b', deviceName: 'office' }),
        session({ sessionId: '2', projectPath: '/a/b', deviceName: 'MacBook Pro' }),
      ],
      [
        { ...mac, connected: false },
        { deviceId: 'dev-office', name: 'office', connected: true },
      ],
    );
    expect(offlineMac.map((g) => [g.name, g.online])).toEqual([
      ['MacBook Pro', false],
      ['office', true],
    ]);
  });
});

describe('deviceConnected and liveDevice', () => {
  it('is online through a live owner even without a device record', () => {
    const live = [session({ sessionId: '1', projectPath: '/a', ownerConnected: true })];
    expect(deviceConnected('MacBook Pro', live, [])).toBe(true);
    expect(
      deviceConnected('MacBook Pro', [session({ sessionId: '2', projectPath: '/a' })], []),
    ).toBe(false);
  });

  it('finds the device by name or by owner id', () => {
    const owned = [session({ sessionId: '1', projectPath: '/a', ownerDeviceId: 'dev-mac' })];
    expect(liveDevice('renamed', owned, [mac])?.deviceId).toBe('dev-mac');
    expect(liveDevice('nobody', [], [mac])).toBeNull();
  });
});

describe('buildTree with watched projects', () => {
  it('adds a watched directory that has no session yet, once', () => {
    const tree = buildTree([session({ sessionId: '1', projectPath: '/a/hoplet' })], [mac], {
      'dev-mac': ['/a/hoplet', '/a/new-app/'],
    });
    expect(tree[0]?.dirs.map((d) => [d.name, d.sessions.length])).toEqual([
      ['hoplet', 1],
      ['new-app', 0],
    ]);
  });
});

describe('sessionTargets', () => {
  it('offers each machine with its directories', () => {
    const targets = sessionTargets(
      buildTree([session({ sessionId: '1', projectPath: '/a/hoplet' })], [mac]),
    );
    expect(targets).toEqual([
      {
        deviceName: 'MacBook Pro',
        deviceId: 'dev-mac',
        online: true,
        providers: [],
        dirs: [{ path: '/a/hoplet', name: 'hoplet' }],
      },
    ]);
  });
});

describe('compactRel', () => {
  it('reads now, minutes, hours and days', () => {
    const now = 10 * 86_400_000;
    expect(compactRel(now - 20_000, now)).toBe('now');
    expect(compactRel(now - 5 * 60_000, now)).toBe('5m');
    expect(compactRel(now - 3 * 3_600_000, now)).toBe('3h');
    expect(compactRel(now - 4 * 86_400_000, now)).toBe('4d');
  });
});
