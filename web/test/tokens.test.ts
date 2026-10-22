import { describe, expect, it } from 'vitest';
import { ApiError } from '../src/lib/errors.ts';
import { setLanguage } from '../src/lib/i18n.ts';
import {
  answersOf,
  capabilitiesOf,
  checkForm,
  daysOf,
  emptyForm,
  expiringSoon,
  holdsMachine,
  holdsPartOf,
  holdsProject,
  presetOf,
  relativeText,
  toggleMachine,
  toggleProject,
  toRequest,
} from '../src/lib/tokens.ts';
import { infoOf, issueProblem } from '../src/lib/use-tokens.ts';

const NOW = Date.UTC(2026, 9, 22, 12);
const DAY = 86_400_000;

describe('the issue form', () => {
  it('starts read only, without answer, for every machine, for 90 days', () => {
    const form = emptyForm();
    expect(form).toMatchObject({ preset: 'read', answer: false, scope: 'all', expiry: '90' });
    expect(daysOf(form)).toBe(90);
  });

  it('turns a preset and the answer switch into capabilities', () => {
    expect(capabilitiesOf({ preset: 'read', answer: false })).toEqual(['read']);
    expect(capabilitiesOf({ preset: 'send', answer: false })).toEqual(['read', 'send']);
    expect(capabilitiesOf({ preset: 'create', answer: true })).toEqual([
      'read',
      'send',
      'create',
      'answer',
    ]);
    expect(presetOf({ capabilities: ['read', 'send', 'answer'] })).toBe('send');
    expect(answersOf({ capabilities: ['read', 'answer'] })).toBe(true);
  });

  it('accepts a custom life of 1 to 365 days and nothing else', () => {
    const custom = (customDays: string) => daysOf({ expiry: 'custom', customDays });
    expect(custom('1')).toBe(1);
    expect(custom(' 365 ')).toBe(365);
    expect(custom('0')).toBeNull();
    expect(custom('366')).toBeNull();
    expect(custom('1.5')).toBeNull();
    expect(custom('')).toBeNull();
  });

  it('needs a name, a place for "Only these" and a valid life', () => {
    const form = { ...emptyForm(), name: '  ', scope: 'only' as const, expiry: 'custom' as const };
    expect(checkForm(form)).toEqual({ ok: false, errors: { name: true, allow: true, days: true } });
    expect(toRequest(form)).toBeNull();
    expect(checkForm({ ...emptyForm(), name: 'x'.repeat(81) }).errors.name).toBe(true);
  });

  it('builds the request: trimmed name, no allow for every machine, the list for "Only these"', () => {
    expect(toRequest({ ...emptyForm(), name: ' Bot ' })).toEqual({
      name: 'Bot',
      grant: { capabilities: ['read'] },
      expiresInDays: 90,
    });
    const only = toRequest({
      ...emptyForm(),
      name: 'Bot',
      preset: 'send',
      scope: 'only',
      allow: [{ deviceId: 'a', path: '/p' }],
    });
    expect(only?.grant).toEqual({
      capabilities: ['read', 'send'],
      allow: [{ deviceId: 'a', path: '/p' }],
    });
  });
});

describe('the allow-list', () => {
  const siblings = ['/a', '/b', '/c'];

  it('ticks a machine whole and takes it out again', () => {
    const whole = toggleMachine([], 'm');
    expect(whole).toEqual([{ deviceId: 'm' }]);
    expect(holdsMachine(whole, 'm')).toBe(true);
    expect(holdsProject(whole, 'm', '/a')).toBe(true);
    expect(toggleMachine(whole, 'm')).toEqual([]);
  });

  it('shows a machine with some projects as partly held, and clears it on the next tick', () => {
    const some = toggleProject([], 'm', '/a', siblings);
    expect(holdsPartOf(some, 'm')).toBe(true);
    expect(holdsMachine(some, 'm')).toBe(false);
    expect(toggleMachine(some, 'm')).toEqual([]);
  });

  it('keeps the other projects when one is taken out of a whole machine', () => {
    const rest = toggleProject([{ deviceId: 'm' }], 'm', '/b', siblings);
    expect(rest).toEqual([
      { deviceId: 'm', path: '/a' },
      { deviceId: 'm', path: '/c' },
    ]);
  });
});

describe('times', () => {
  it('words a moment against now', () => {
    setLanguage('en');
    expect(relativeText(NOW - 10_000, NOW)).toBe('just now');
    expect(relativeText(NOW - 4 * 60_000, NOW)).toBe('4 min ago');
    expect(relativeText(NOW - 2 * 3_600_000, NOW)).toBe('2 h ago');
    expect(relativeText(NOW - DAY, NOW)).toBe('yesterday');
    expect(relativeText(NOW + 61 * DAY, NOW)).toBe('in 61 days');
  });

  it('marks an active token that expires within 7 days', () => {
    expect(expiringSoon({ state: 'active', expiresAt: NOW + 6 * DAY }, NOW)).toBe(true);
    expect(expiringSoon({ state: 'active', expiresAt: NOW + 8 * DAY }, NOW)).toBe(false);
    expect(expiringSoon({ state: 'revoked', expiresAt: NOW + DAY }, NOW)).toBe(false);
  });
});

describe('what the hook hands on', () => {
  it('leaves the secret out of the record the list keeps', () => {
    const info = infoOf({
      id: 'tok_1',
      kind: 'scoped',
      name: 'Bot',
      grant: { capabilities: ['read'] },
      createdAt: 1,
      expiresAt: 2,
      revokedAt: null,
      lastUsedAt: null,
      state: 'active',
      token: 'hpt_secret',
    });
    expect(info).not.toHaveProperty('token');
    expect(JSON.stringify(info)).not.toContain('hpt_secret');
  });

  it('gives the limit of active tokens its own words', () => {
    setLanguage('en');
    expect(issueProblem(new ApiError(413, 'too-large'))).toBe(
      'The most Hoplet allows, 100 tokens, are active. Revoke one before issuing another.',
    );
  });
});
