import { describe, expect, it } from 'vitest';
import {
  appendHistory,
  archName,
  cpuSeries,
  diskText,
  memoryGb,
  memorySeries,
  percent,
  readingWarn,
  shade,
  sortSessions,
  sparkPoints,
  stack,
} from '../src/screens/settings/resources-model.ts';

const GB = 1024 ** 3;
const sample = (over: Partial<Parameters<typeof readingWarn>[0]> = {}) => ({
  at: 1,
  cpu: 10,
  memUsed: 20 * GB,
  memTotal: 32 * GB,
  diskFree: 184e9,
  diskTotal: 994e9,
  ...over,
});
const sess = (id: string, rss: number) => ({
  sessionId: id,
  title: id,
  projectPath: null,
  cpu: 1,
  rss,
  children: 0,
});

describe('formatting', () => {
  it('rounds memory to one decimal without a trailing zero', () => {
    expect(memoryGb(20.2 * GB)).toBe(20.2);
    expect(memoryGb(32 * GB)).toBe(32);
  });
  it('writes disk space in GB, and in TB from 1000 GB', () => {
    expect(diskText(184e9)).toBe('184 GB');
    expect(diskText(1e12)).toBe('1 TB');
    expect(diskText(1.5e12)).toBe('1.5 TB');
  });
  it('rounds a share and survives an empty whole', () => {
    expect(percent(1, 3)).toBe(33);
    expect(percent(5, 0)).toBe(0);
  });
  it('names the architecture', () => {
    expect(archName('amd64')).toBe('x86_64');
    expect(archName('arm64')).toBe('arm64');
    expect(archName('')).toBeUndefined();
  });
});

describe('readingWarn', () => {
  it('warns on memory above 90 % used, not at it', () => {
    expect(readingWarn(sample({ memUsed: 29 * GB })).memory).toBe(true);
    expect(readingWarn(sample({ memUsed: 28.8 * GB })).memory).toBe(false);
  });
  it('warns on disk under 10 % free, not at it', () => {
    expect(readingWarn(sample({ diskFree: 99e9, diskTotal: 1000e9 })).disk).toBe(true);
    expect(readingWarn(sample({ diskFree: 100e9, diskTotal: 1000e9 })).disk).toBe(false);
  });
  it('does not warn without totals', () => {
    expect(readingWarn(sample({ memTotal: 0, diskTotal: 0 }))).toEqual({
      memory: false,
      disk: false,
    });
  });
});

describe('sortSessions', () => {
  it('puts the largest first and keeps the order of equals', () => {
    const out = sortSessions([sess('a', 1), sess('b', 3), sess('c', 3), sess('d', 2)]);
    expect(out.map((s) => s.sessionId)).toEqual(['b', 'c', 'd', 'a']);
  });
});

describe('stack', () => {
  const s = [sess('a', 0), sess('b', 0)];
  it('derives the other processes as used minus the sessions', () => {
    const r = stack(s, [10, 20], 100, 50);
    expect(r.other).toBe(20);
    expect(r.segments.map((x) => x.width)).toEqual([10, 20]);
    expect(r.otherWidth).toBe(20);
  });
  it('never lets other go negative or the widths pass the bar', () => {
    const r = stack(s, [70, 60], 100, 100);
    expect(r.other).toBe(0);
    const sum = r.segments.reduce((a, x) => a + x.width, 0) + r.otherWidth;
    expect(sum).toBeLessThanOrEqual(100);
  });
  it('shades four steps and repeats the last', () => {
    expect([0, 1, 2, 3, 4, 9].map(shade)).toEqual([0, 1, 2, 3, 3, 3]);
  });
  it('gives an empty bar for a zero total', () => {
    expect(stack(s, [1, 2], 0, 0)).toEqual({ segments: [], other: 0, otherWidth: 0 });
  });
});

describe('sparkPoints', () => {
  it('needs two values', () => {
    expect(sparkPoints([], 120, 24)).toBe('');
    expect(sparkPoints([5], 120, 24)).toBe('');
  });
  it('spans the width and keeps 2 px free; a flat line sits at the bottom', () => {
    expect(sparkPoints([0, 100], 120, 24)).toBe('0.0,22.0 120.0,2.0');
    expect(sparkPoints([50, 50], 120, 24)).toBe('0.0,22.0 120.0,22.0');
  });
  it('uses a span of at least 8 so noise stays flat', () => {
    expect(sparkPoints([10, 14], 10, 24)).toBe('0.0,22.0 10.0,12.0');
  });
});

describe('series', () => {
  it('skips readings without CPU', () => {
    expect(cpuSeries([sample({ cpu: null }), sample({ cpu: 7 })])).toEqual([7]);
  });
  it('turns memory into percents', () => {
    expect(memorySeries([sample({ memUsed: 16 * GB })])).toEqual([50]);
  });
});

describe('appendHistory', () => {
  it('appends per session and drops sessions that are gone', () => {
    const one = appendHistory({}, [sess('a', 1), sess('b', 2)], true);
    const two = appendHistory(one, [sess('a', 3)], true);
    expect(two).toEqual({ a: [1, 3] });
  });
  it('adds nothing for a poll without a new reading', () => {
    const one = appendHistory({}, [sess('a', 1)], true);
    expect(appendHistory(one, [sess('a', 9)], false)).toBe(one);
  });
  it('keeps at most the ring size', () => {
    let h = {};
    for (let i = 0; i < 300; i++) h = appendHistory(h, [sess('a', i)], true);
    expect((h as Record<string, number[]>).a).toHaveLength(240);
  });
});
