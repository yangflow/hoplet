import { describe, expect, it } from 'vitest';
import {
  anchorAt,
  offsetsOf,
  restoreScroll,
  rowAt,
  visibleRange,
} from '../src/screens/cockpit/virtual.ts';

describe('virtual layout', () => {
  const heights = [30, 100, 30, 50, 200];
  const offsets = offsetsOf(heights);

  it('stacks rows and ends with the total height', () => {
    expect(offsets).toEqual([0, 30, 130, 160, 210, 410]);
  });

  it('finds the row at a position', () => {
    expect(rowAt(offsets, 0)).toBe(0);
    expect(rowAt(offsets, 29)).toBe(0);
    expect(rowAt(offsets, 30)).toBe(1);
    expect(rowAt(offsets, 209)).toBe(3);
    expect(rowAt(offsets, 5000)).toBe(4);
  });

  it('mounts only the rows in view and the overscan', () => {
    const many = offsetsOf(Array.from({ length: 10_000 }, () => 40));
    const [start, end] = visibleRange(many, 200_000, 800, 500);
    expect(end - start).toBeLessThan(50);
    expect(start * 40).toBeLessThanOrEqual(200_000 - 500);
    expect(end * 40).toBeGreaterThanOrEqual(200_800 + 500);
  });

  it('always mounts at least one row of a non-empty list', () => {
    expect(visibleRange(offsets, 0, 0, 0)[1]).toBeGreaterThan(0);
    expect(visibleRange(offsetsOf([]), 0, 800, 500)).toEqual([0, 0]);
  });

  it('puts the reader back where they were when rows are prepended', () => {
    const keys = ['a', 'b', 'c', 'd', 'e'];
    const anchor = anchorAt(keys, offsets, 140);
    expect(anchor).toEqual({ key: 'c', offset: -10 });
    // Two rows of 40 and 60 arrive above.
    const nextKeys = ['x', 'y', ...keys];
    const nextOffsets = offsetsOf([40, 60, ...heights]);
    expect(restoreScroll(nextKeys, nextOffsets, anchor as NonNullable<typeof anchor>)).toBe(240);
  });

  it('puts the reader back when rows above are dropped, and gives up when theirs is gone', () => {
    const keys = ['a', 'b', 'c', 'd', 'e'];
    const anchor = anchorAt(keys, offsets, 170);
    const rest = offsetsOf(heights.slice(2));
    expect(restoreScroll(keys.slice(2), rest, anchor as NonNullable<typeof anchor>)).toBe(40);
    expect(restoreScroll(['x'], offsetsOf([10]), anchor as NonNullable<typeof anchor>)).toBeNull();
  });
});
