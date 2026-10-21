import type { UsageRow } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  buildChart,
  costToShow,
  customRange,
  dayKey,
  dayKeys,
  dayStart,
  formatCost,
  formatPrice,
  formatShare,
  formatTokens,
  groupRows,
  isCurrent,
  niceMax,
  parsePrice,
  priceFields,
  priceRows,
  rangeDays,
  rangeOf,
  shiftRange,
  unpricedModels,
  unpricedTokens,
  withOverride,
  withoutOverride,
} from '../src/lib/usage.ts';

const price = { input: 1, output: 2, cacheWrite: 3, cacheRead: 4 };
const row = (keys: [string, string?][], tokens: number, cost: number | null): UsageRow => ({
  keys: keys.map(([value, label]) => ({ value, label: label ?? value })),
  input: tokens,
  output: 0,
  cacheWrite: 0,
  cacheRead: 0,
  messages: 1,
  estimatedCostUsd: cost,
  costComplete: cost !== null,
});

describe('formatTokens', () => {
  it('keeps three significant digits', () => {
    expect(formatTokens(842)).toBe('842');
    expect(formatTokens(9800)).toBe('9.8k');
    expect(formatTokens(12_400)).toBe('12.4k');
    expect(formatTokens(214_000)).toBe('214k');
    expect(formatTokens(2_420_000)).toBe('2.42M');
    expect(formatTokens(18_400_000)).toBe('18.4M');
    expect(formatTokens(1_200_000_000)).toBe('1.2B');
  });
  it('rolls 999.6k over to 1M', () => {
    expect(formatTokens(999_600)).toBe('1M');
  });
});

describe('cost and share', () => {
  it('formats cost, small cost and no price', () => {
    expect(formatCost(8.123)).toBe('$8.12');
    expect(formatCost(0.001)).toBe('<$0.01');
    expect(formatCost(0)).toBe('$0.00');
    expect(formatCost(null)).toBe('—');
  });
  it('hides the cost for tokens only', () => {
    expect(costToShow({ estimatedCostUsd: 3 }, true)).toBeNull();
    expect(costToShow({ estimatedCostUsd: 3 }, false)).toBe('$3.00');
    expect(costToShow({ estimatedCostUsd: null }, false)).toBe('—');
  });
  it('formats a share', () => {
    expect(formatShare(38, 100)).toBe('38%');
    expect(formatShare(1, 1000)).toBe('<1%');
    expect(formatShare(0, 100)).toBe('0%');
    expect(formatShare(5, 0)).toBe('0%');
  });
});

describe('ranges', () => {
  const at = Date.UTC(2026, 9, 18, 12); // Sunday
  it('takes Monday-first weeks', () => {
    const w = rangeOf('week', at, 0);
    expect(dayKey(w.from, 0)).toBe('2026-10-12');
    expect(dayKey(w.to, 0)).toBe('2026-10-19');
    expect(rangeDays(w, 0)).toBe(7);
  });
  it('takes the day and the month', () => {
    expect(dayKey(rangeOf('day', at, 0).to, 0)).toBe('2026-10-19');
    const m = rangeOf('month', at, 0);
    expect(dayKey(m.from, 0)).toBe('2026-10-01');
    expect(dayKey(m.to, 0)).toBe('2026-11-01');
  });
  it('follows the UTC offset', () => {
    // 20:00 UTC on the 18th is already the 19th at +08:00.
    const late = Date.UTC(2026, 9, 18, 20);
    expect(dayKey(late, 480)).toBe('2026-10-19');
    const d = rangeOf('day', late, 480);
    expect(d.from).toBe(Date.UTC(2026, 9, 18, 16));
    expect(dayStart('2026-10-19', 480)).toBe(d.from);
  });
  it('shifts back and forth', () => {
    const w = rangeOf('week', at, 0);
    expect(dayKey(shiftRange(w, -1, 0).from, 0)).toBe('2026-10-05');
    expect(shiftRange(shiftRange(w, 1, 0), -1, 0)).toEqual(w);
    const m = rangeOf('month', at, 0);
    expect(dayKey(shiftRange(m, -1, 0).from, 0)).toBe('2026-09-01');
    expect(dayKey(shiftRange(m, 1, 0).to, 0)).toBe('2026-12-01');
    const c = customRange('2026-10-05', '2026-10-18', 0);
    expect(c && rangeDays(c, 0)).toBe(14);
    expect(c && dayKey(shiftRange(c, 1, 0).from, 0)).toBe('2026-10-19');
  });
  it('builds a custom range or refuses', () => {
    expect(customRange('2026-10-05', '2026-10-05', 0)?.kind).toBe('custom');
    expect(customRange('2026-10-06', '2026-10-05', 0)).toBeNull();
    expect(customRange('nope', '2026-10-05', 0)).toBeNull();
    expect(customRange('2026-02-30', '2026-03-05', 0)).toBeNull();
  });
  it('lists days and knows the current range', () => {
    const w = rangeOf('week', at, 0);
    expect(dayKeys(w, 0)).toHaveLength(7);
    expect(dayKeys(w, 0)[0]).toBe('2026-10-12');
    expect(isCurrent(w, at)).toBe(true);
    expect(isCurrent(shiftRange(w, -1, 0), at)).toBe(false);
    const long = customRange('2026-01-01', '2026-12-31', 0);
    expect(long && dayKeys(long, 0)).toEqual([]);
  });
});

describe('chart', () => {
  const rows = [
    row([['2026-10-12'], ['a']], 100, 1),
    row([['2026-10-12'], ['b']], 50, 0.5),
    row([['2026-10-13'], ['a']], 100, 1),
    row([['2026-10-13'], ['c']], 30, null),
    row([['2026-10-13'], ['d']], 20, 0.1),
    row([['2026-10-13'], ['e']], 10, 0.1),
  ];
  it('keeps three models and folds the rest', () => {
    const c = buildChart(rows, ['2026-10-12', '2026-10-13', '2026-10-14']);
    expect(c.models.map((m) => m.id)).toEqual(['a', 'b', 'c', '']);
    expect(c.models[3]?.step).toBe('ot');
    expect(c.days.map((d) => d.tokens)).toEqual([150, 160, 0]);
    expect(c.days[1]?.segments.map((s) => s.step)).toEqual(['s0', 's2', 'ot']);
    expect(c.days[0]?.costUsd).toBe(1.5);
    expect(c.max).toBe(200);
  });
  it('has a null cost for a day with no price and draws only used days without columns', () => {
    const c = buildChart([row([['2026-10-12'], ['x']], 10, null)], []);
    expect(c.days).toHaveLength(1);
    expect(c.days[0]?.costUsd).toBeNull();
  });
  it('rounds the axis up', () => {
    expect(niceMax(0)).toBe(1);
    expect(niceMax(3_200_000)).toBe(5_000_000);
    expect(niceMax(1_000)).toBe(1_000);
    expect(niceMax(7)).toBe(10);
  });
});

describe('groups', () => {
  it('maps rows and marks incomplete cost', () => {
    const r = row(
      [
        ['s1', 'Fix login'],
        ['p', 'web'],
      ],
      10,
      1,
    );
    const g = groupRows({ rows: [{ ...r, costComplete: false }] });
    expect(g).toEqual([
      { id: 's1\u0000p', name: 'Fix login', detail: 'web', tokens: 10, costUsd: 1, unpriced: true },
    ]);
  });
  it('sums the tokens without a price', () => {
    const rows = [row([['x']], 10, null), row([['y']], 5, 1), row([['d'], ['x']], 7, null)];
    expect(unpricedTokens(rows)).toBe(17);
    expect(unpricedModels(rows)).toEqual([{ model: 'x', label: 'x', tokens: 7 + 10 }]);
  });
});

describe('prices', () => {
  const prices = {
    defaults: { 'm-a': price, 'm-b': price },
    overrides: { 'm-b': { ...price, input: 9 } },
  };
  it('lists unpriced first, then yours, then built in', () => {
    const rows = priceRows(prices, [
      { model: 'zzz', tokens: 5 },
      { model: 'm-a-20250101', tokens: 3 },
    ]);
    expect(rows.map((r) => [r.model, r.source])).toEqual([
      ['zzz', 'none'],
      ['m-b', 'yours'],
      ['m-a', 'builtin'],
    ]);
    expect(rows[0]?.tokens).toBe(5);
  });
  it('round-trips fields', () => {
    expect(priceFields(null)).toEqual({ input: '', output: '', cacheWrite: '', cacheRead: '' });
    expect(parsePrice(priceFields(price))).toEqual(price);
  });
  it('parses an empty field as 0 and refuses bad ones', () => {
    const f = { input: '3', output: ' 15 ', cacheWrite: '', cacheRead: '0.3' };
    expect(parsePrice(f)).toEqual({ input: 3, output: 15, cacheWrite: 0, cacheRead: 0.3 });
    expect(parsePrice({ ...f, input: 'abc' })).toBeNull();
    expect(parsePrice({ ...f, input: '-1' })).toBeNull();
    expect(parsePrice({ ...f, input: '1000001' })).toBeNull();
  });
  it('formats a price', () => {
    expect(formatPrice(15)).toBe('15.00');
    expect(formatPrice(0.075)).toBe('0.075');
  });
  it('sets and clears an override', () => {
    const set = withOverride({}, 'm', price);
    expect(set).toEqual({ m: price });
    expect(withoutOverride(set, 'm')).toEqual({});
  });
});
