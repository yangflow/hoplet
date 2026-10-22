import { describe, expect, it } from 'vitest';
import { SECTIONS, sectionOf } from '../src/screens/settings/index.tsx';
import {
  defaultMachine,
  providerUse,
  versionBadge,
} from '../src/screens/settings/providers-model.ts';

const mac = { deviceId: 'dev-mac' };
const studio = { deviceId: 'dev-studio' };

const s = (
  provider: 'claude' | 'codex',
  updatedAt: number,
  owner?: string,
  deviceName = 'MacBook Pro',
) => ({
  provider,
  updatedAt,
  deviceName,
  ...(owner ? { ownerDeviceId: owner } : {}),
});

describe('provider use', () => {
  const sessions = [
    s('claude', 100, 'dev-mac'),
    s('claude', 300, 'dev-mac'),
    s('codex', 200, 'dev-mac'),
    s('claude', 900, 'dev-studio', 'studio'),
    s('claude', 50, undefined, 'other'),
  ];

  it('counts per device and provider and takes the newest update', () => {
    expect(providerUse(sessions, mac, 'claude')).toEqual({ sessions: 2, lastUsed: 300 });
    expect(providerUse(sessions, mac, 'codex')).toEqual({ sessions: 1, lastUsed: 200 });
    expect(providerUse(sessions, studio, 'claude')).toEqual({ sessions: 1, lastUsed: 900 });
  });

  it('does not count a session without an owner for any machine', () => {
    const none = [s('claude', 50, undefined, 'MacBook Pro')];
    expect(providerUse(none, mac, 'claude')).toEqual({ sessions: 0, lastUsed: undefined });
  });

  it('counts an archived session', () => {
    const archived = [{ ...s('claude', 70, 'dev-mac'), archivedAt: 80 }];
    expect(providerUse(archived, mac, 'claude')).toEqual({ sessions: 1, lastUsed: 70 });
  });

  it('is empty without sessions', () => {
    expect(providerUse([], mac, 'codex')).toEqual({ sessions: 0, lastUsed: undefined });
  });
});

describe('version badge', () => {
  it('is Tested, Newer, or nothing when the version is unknown', () => {
    expect(versionBadge({ version: '2.1.42', tested: true })).toBe('tested');
    expect(versionBadge({ version: '2.2.0', tested: false })).toBe('newer');
    expect(versionBadge({})).toBeNull();
    expect(versionBadge({ version: '2.2.0' })).toBeNull();
  });
});

describe('default machine', () => {
  it('is the first connected one, else the first', () => {
    expect(
      defaultMachine([
        { deviceId: 'a', connected: false },
        { deviceId: 'b', connected: true },
      ]),
    ).toBe('b');
    expect(defaultMachine([{ deviceId: 'a', connected: false }])).toBe('a');
    expect(defaultMachine([])).toBeUndefined();
  });
});

describe('section', () => {
  it('sits between Machines and Notifications and is addressed as providers', () => {
    expect(SECTIONS.map((x) => x.id)).toEqual([
      'appearance',
      'machines',
      'providers',
      'notifications',
      'rules',
      'hooks',
      'audit',
      'tokens',
      'usage',
      'templates',
    ]);
    expect(sectionOf('providers')).toBe('providers');
  });
});
