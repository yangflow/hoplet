import type { NotifyPreferences } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  deliveryText,
  deviceLabel,
  EMPTY_PREFERENCES,
  effectiveChannels,
  hostHint,
  isOverridden,
  keyBytes,
  overrideCount,
  projectChoices,
  projectName,
  resetScope,
  sameEvents,
  toggleChannel,
  toggleWebhookEvent,
  webhookEventsText,
} from '../src/screens/settings/notify-model.ts';

const ALL = { project: null };
const P = '/Users/me/code/billing';
const IN_P = { project: P };

describe('effectiveChannels', () => {
  it('follows the preset: every channel for an event it covers, none for one it does not', () => {
    expect(effectiveChannels(EMPTY_PREFERENCES, 'permission', ALL)).toEqual([
      'push',
      'webhook',
      'native',
    ]);
    expect(effectiveChannels(EMPTY_PREFERENCES, 'turn-done', ALL)).toEqual([]);
    expect(
      effectiveChannels({ ...EMPTY_PREFERENCES, preset: 'needs-me-and-done' }, 'turn-done', ALL),
    ).toEqual(['push', 'webhook', 'native']);
    expect(effectiveChannels({ ...EMPTY_PREFERENCES, preset: 'off' }, 'failure', ALL)).toEqual([]);
  });

  it('lets the project override beat the event override beat the preset, as the core does', () => {
    const prefs: NotifyPreferences = {
      ...EMPTY_PREFERENCES,
      events: { failure: { channels: ['push'] } },
      projects: { [P]: { preset: 'off', events: { permission: { channels: ['webhook'] } } } },
    };
    expect(effectiveChannels(prefs, 'failure', ALL)).toEqual(['push']);
    expect(effectiveChannels(prefs, 'failure', IN_P)).toEqual([]);
    expect(effectiveChannels(prefs, 'permission', IN_P)).toEqual(['webhook']);
  });
});

describe('toggleChannel', () => {
  it('stores an override that differs from the preset', () => {
    const next = toggleChannel(EMPTY_PREFERENCES, 'permission', 'webhook', ALL);
    expect(next.events).toEqual({ permission: { channels: ['push', 'native'] } });
    expect(isOverridden(next, 'permission', ALL)).toBe(true);
    expect(overrideCount(next)).toBe(1);
  });

  it('drops the override when the choice is the preset again', () => {
    const once = toggleChannel(EMPTY_PREFERENCES, 'permission', 'webhook', ALL);
    expect(toggleChannel(once, 'permission', 'webhook', ALL)).toEqual(EMPTY_PREFERENCES);
  });

  it('keeps a project override apart from the global ones, and prunes an empty project', () => {
    const once = toggleChannel(EMPTY_PREFERENCES, 'turn-done', 'push', IN_P);
    expect(once.events).toEqual({});
    expect(once.projects[P]?.events).toEqual({ 'turn-done': { channels: ['push'] } });
    expect(overrideCount(once)).toBe(1);
    expect(toggleChannel(once, 'turn-done', 'push', IN_P).projects).toEqual({});
  });
});

describe('resetScope', () => {
  it('clears the overrides of one scope and keeps the level', () => {
    const prefs: NotifyPreferences = {
      preset: 'everything',
      events: { failure: { channels: ['push'] } },
      projects: { [P]: { events: { failure: { channels: [] } } } },
      quietHours: null,
    };
    expect(resetScope(prefs, IN_P).projects).toEqual({});
    expect(resetScope(prefs, IN_P).events).toEqual(prefs.events);
    expect(resetScope(prefs, ALL).events).toEqual({});
    expect(resetScope(prefs, ALL).preset).toBe('everything');
  });
});

describe('projects', () => {
  it('names a project by the last segment of its path', () => {
    expect(projectName('/Users/me/code/billing/')).toBe('billing');
    expect(projectName('C:\\work\\api')).toBe('api');
  });
  it('offers the projects of sessions and of preferences once, by name', () => {
    const prefs = { ...EMPTY_PREFERENCES, projects: { '/b/zeta': { events: {} } } };
    expect(projectChoices(prefs, ['/a/alpha', '/b/zeta'])).toEqual(['/a/alpha', '/b/zeta']);
  });
});

describe('deviceLabel', () => {
  it('reads the browser and the system from a user agent', () => {
    expect(
      deviceLabel(
        'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36',
      ),
    ).toBe('Chrome on macOS');
    expect(
      deviceLabel('Mozilla/5.0 (Windows NT 10.0; Win64; x64) Gecko/20100101 Firefox/127.0'),
    ).toBe('Firefox on Windows');
    expect(deviceLabel('')).toBe('Browser');
  });
});

describe('keyBytes', () => {
  it('decodes an unpadded base64url key', () => {
    expect([...keyBytes('AQID_w')]).toEqual([1, 2, 3, 255]);
  });
});

describe('hostHint', () => {
  it('shows the host and hides the path, which may be a secret', () => {
    expect(hostHint('https://hooks.example.com/services/T0/B0/xxxx')).toBe('hooks.example.com/…');
    expect(hostHint('https://home.example.net')).toBe('home.example.net');
    expect(hostHint('not a url')).toBe('…');
  });
});

describe('deliveryText', () => {
  const now = 10 * 3_600_000;
  it('says that nothing was sent yet', () => {
    expect(deliveryText(undefined, now)).toBe('Not sent yet');
  });
  it('gives the age of a success', () => {
    expect(deliveryText({ at: now - 30_000, ok: true }, now)).toBe('Delivered just now');
    expect(deliveryText({ at: now - 2 * 60_000, ok: true }, now)).toBe('Delivered 2 min ago');
    expect(deliveryText({ at: now - 3 * 3_600_000, ok: true }, now)).toBe('Delivered 3 h ago');
  });
  it('gives the reason of a failure', () => {
    expect(deliveryText({ at: now, ok: false, error: 'timeout' }, now)).toBe('Failed · timeout');
    expect(deliveryText({ at: now, ok: false, error: 'http-502' }, now)).toBe('Failed · HTTP 502');
  });
});

describe('webhook events', () => {
  it('switches an event and keeps the canonical order', () => {
    expect(toggleWebhookEvent(['permission', 'plan', 'failure'], 'turn-done')).toEqual([
      'permission',
      'plan',
      'turn-done',
      'failure',
    ]);
    expect(toggleWebhookEvent(['permission', 'plan', 'failure'], 'plan')).toEqual([
      'permission',
      'failure',
    ]);
  });

  it('never switches the last event off', () => {
    expect(toggleWebhookEvent(['failure'], 'failure')).toEqual(['failure']);
  });

  it('lists the events in words and compares sets regardless of order', () => {
    expect(webhookEventsText(['failure', 'permission', 'turn-done'])).toBe(
      'Permission, Finished, Failed',
    );
    expect(sameEvents(['plan', 'permission'], ['permission', 'plan'])).toBe(true);
    expect(sameEvents(['plan'], ['permission', 'plan'])).toBe(false);
  });
});
