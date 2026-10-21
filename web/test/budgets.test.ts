import { describe, expect, it } from 'vitest';
import {
  amountText,
  barKind,
  barPercent,
  covers,
  dateText,
  draftOf,
  endDate,
  leftOf,
  newDraft,
  nextDate,
  parseAlert,
  parseLimit,
  pastAlert,
  problemOf,
  projectLabel,
  resetText,
  scopeName,
  shortName,
  statusOf,
  toBody,
} from '../src/lib/budgets.ts';
import {
  B1,
  B2,
  B3,
  B4,
  B4_ALLOWED,
  B4_OK,
  DOCS_PATH,
  HOPLET_PATH,
} from '../src/screens/settings/usage-fixtures.ts';

const devices = [{ deviceId: 'dev-studio', name: 'studio' }];

describe('how a budget row reads', () => {
  it('names the status of each design row', () => {
    expect(statusOf(B1)).toBe('ok');
    expect(statusOf(B2)).toBe('near');
    expect(statusOf(B3)).toBe('over');
    expect(statusOf(B4)).toBe('paused');
    expect(statusOf(B4_OK)).toBe('ok');
    expect(statusOf(B4_ALLOWED)).toBe('allowed');
  });

  it('colours the bar red only where new prompts are refused', () => {
    expect(barKind('paused')).toBe('stop');
    expect(barKind('over')).toBe('warn');
    expect(barKind('near')).toBe('warn');
    expect(barKind('allowed')).toBe('warn');
    expect(barKind('ok')).toBe('');
  });

  it('caps the fill at the full bar and writes amounts in the budget unit', () => {
    expect(barPercent(B3)).toBe(100);
    expect(barPercent({ percent: -3 })).toBe(0);
    expect(barPercent(B1)).toBe(61);
    expect(amountText('cost', 10.6)).toBe('$10.60');
    expect(amountText('tokens', 10_300_000)).toBe('10.3M');
    expect(leftOf(B4)).toBeCloseTo(0.6);
    expect(leftOf(B4_OK)).toBeCloseTo(2.6);
  });

  it('counts a session by scope', () => {
    const session = { projectPath: HOPLET_PATH, ownerDeviceId: 'dev-studio' };
    expect(covers(B1, session)).toBe(true);
    expect(covers(B2, session)).toBe(true);
    expect(covers(B3, session)).toBe(true);
    expect(covers(B4, session)).toBe(false);
    expect(covers(B4, { projectPath: DOCS_PATH })).toBe(true);
    expect(covers(B3, { projectPath: HOPLET_PATH })).toBe(false);
  });

  it('is past the alert line from the alert state on', () => {
    expect([B1, B2, B3, B4, B4_ALLOWED].map(pastAlert)).toEqual([false, true, true, true, true]);
  });
});

describe('the dates of a period', () => {
  it('writes the reset and the last day the way the design does', () => {
    expect(resetText(B1)).toBe('Nov 1');
    expect(resetText(B2)).toBe('Mon, Oct 26');
    expect(resetText(B3)).toBe('at midnight');
    expect(endDate(B4)).toBe('Oct 31');
    expect(nextDate(B4)).toBe('Nov 1');
  });

  it('follows the budget offset and the language', () => {
    const end = Date.UTC(2026, 10, 1);
    expect(dateText(end, 0)).toBe('Nov 1');
    expect(dateText(end, -120)).toBe('Oct 31');
    expect(dateText(end, 0, true, 'zh-Hans')).toContain('11月1日');
  });

  it('titles a row by scope and a popover line by name and period', () => {
    expect(scopeName(B1)).toBe('Global');
    expect(scopeName(B2)).toBe('Project · hoplet');
    expect(scopeName(B3)).toBe('Machine · studio');
    expect(shortName(B2)).toBe('hoplet · weekly');
    expect(shortName(B1)).toBe('Global · monthly');
    expect(projectLabel('C:\\code\\hoplet\\')).toBe('hoplet');
  });
});

describe('the budget form', () => {
  it('starts as a global monthly cost budget that only warns', () => {
    const d = newDraft();
    expect([d.id, d.scope, d.period, d.unit, d.alert, d.onLimit]).toEqual([
      null,
      'global',
      'month',
      'cost',
      '80',
      'warn',
    ]);
    expect(problemOf(d)).toBe('limit');
  });

  it('shows tokens in millions and takes them back as whole tokens', () => {
    const d = draftOf(B2);
    expect(d.limit).toBe('12');
    expect(d.target).toBe(HOPLET_PATH);
    expect(parseLimit('12', 'tokens')).toBe(12_000_000);
    expect(parseLimit('0.5', 'tokens')).toBe(500_000);
    expect(parseLimit('10,5', 'cost')).toBe(10.5);
    expect(parseLimit('0', 'cost')).toBeNull();
    expect(parseLimit('abc', 'cost')).toBeNull();
    expect(parseLimit('', 'cost')).toBeNull();
  });

  it('takes an alert line from 1 to 100 as a whole number', () => {
    expect(parseAlert('80')).toBe(80);
    expect(parseAlert(' 100 ')).toBe(100);
    expect(parseAlert('0')).toBeNull();
    expect(parseAlert('101')).toBeNull();
    expect(parseAlert('7.5')).toBeNull();
  });

  it('names the first problem in the order of the form', () => {
    expect(problemOf({ ...newDraft(), scope: 'project', limit: '5' })).toBe('target');
    expect(problemOf({ ...newDraft(), limit: '5', alert: '' })).toBe('alert');
    expect(problemOf({ ...newDraft(), limit: '5' })).toBeNull();
  });

  it('makes the body, naming the budget after its target', () => {
    expect(toBody(newDraft(), devices)).toBeNull();
    const d = { ...draftOf(B3), limit: '20', onLimit: 'pause' as const };
    expect(toBody(d, devices)).toEqual({
      name: 'studio',
      scope: { kind: 'device', value: 'dev-studio' },
      period: 'day',
      unit: 'cost',
      amount: 20,
      alertPercent: 80,
      onLimit: 'pause',
      utcOffset: 0,
    });
    expect(toBody(draftOf(B4), devices)?.name).toBe('hoplet-docs');
    expect(toBody({ ...newDraft(), limit: '1' }, devices)?.scope).toEqual({
      kind: 'global',
      value: '',
    });
  });
});
