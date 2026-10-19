import { describe, expect, it } from 'vitest';
import { SECTIONS, sectionOf } from '../src/screens/settings/index.tsx';
import {
  defaultMachine,
  providerUse,
  versionBadge,
} from '../src/screens/settings/providers-model.ts';

const mac = { deviceId: 'dev-mac', name: 'MacBook Pro' };
const studio = { deviceId: 'dev-studio', name: 'studio' };

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

  it('falls back to the device name for a session without an owner', () => {
    const none = [s('claude', 50, undefined, 'MacBook Pro')];
    expect(providerUse(none, mac, 'claude')).toEqual({ sessions: 1, lastUsed: 50 });
    expect(providerUse(none, studio, 'claude')).toEqual({ sessions: 0, lastUsed: undefined });
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
    ]);
    expect(sectionOf('providers')).toBe('providers');
  });
});
