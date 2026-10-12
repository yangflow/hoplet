import type { Entry } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  bytesOf,
  itemsOf,
  LIMITS,
  type Limits,
  PageCache,
  pageOf,
} from '../src/screens/cockpit/page-cache.ts';

const entry = (lineNo: number, blocks = 1, text = 'x'): Entry => ({
  lineNo,
  role: 'assistant',
  blocks: Array.from({ length: blocks }, () => ({ type: 'text' as const, text })),
});

/** A page of `count` lines from `start`, each with `blocks` blocks of `text`. */
function page(start: number, count: number, blocks = 1, text = 'x') {
  return pageOf(
    start,
    start + count,
    Array.from({ length: count }, (_, i) => entry(start + i, blocks, text)),
  );
}

describe('limits', () => {
  it('keeps at most 1,000 items and 8 MiB of line data', () => {
    expect(LIMITS).toEqual({ items: 1000, bytes: 8 * 1024 * 1024 });
  });

  it('counts an item per block and the stored size of elided blocks', () => {
    expect(itemsOf(entry(0, 3))).toBe(3);
    expect(itemsOf({ lineNo: 0, role: 'system', blocks: [] })).toBe(1);
    const elided: Entry = {
      lineNo: 1,
      role: 'user',
      blocks: [{ elided: true, type: 'tool-result', bytes: 5_000_000, preview: 'p', ref: '1/0' }],
    };
    expect(bytesOf(elided)).toBeGreaterThanOrEqual(5_000_000);
  });
});

describe('PageCache', () => {
  it('holds contiguous pages and reports the lines it covers', () => {
    const cache = new PageCache();
    expect(cache.add(page(160, 80))).toBe(true);
    expect(cache.add(page(80, 80))).toBe(true);
    expect(cache.add(page(240, 80))).toBe(true);
    expect([cache.start, cache.end, cache.pageCount]).toEqual([80, 320, 3]);
    expect(cache.entries().map((e) => e.lineNo)).toEqual(
      Array.from({ length: 240 }, (_, i) => 80 + i),
    );
  });

  it('refuses a page that leaves a hole', () => {
    const cache = new PageCache();
    cache.add(page(0, 80));
    expect(cache.add(page(200, 80))).toBe(false);
    expect([cache.start, cache.end]).toEqual([0, 80]);
  });

  it('replaces lines it already holds instead of repeating them', () => {
    const cache = new PageCache();
    cache.add(page(0, 80));
    cache.add(page(80, 80));
    cache.add(page(60, 40, 1, 'again'));
    const lines = cache.entries().map((e) => e.lineNo);
    expect(lines).toEqual(Array.from({ length: 160 }, (_, i) => i));
    expect(new Set(lines).size).toBe(lines.length);
    const fresh = cache.entries().find((e) => e.lineNo === 70)?.blocks[0];
    expect(fresh).toEqual({ type: 'text', text: 'again' });
  });

  it('drops the end farther from the reader when items pass 1,000', () => {
    const cache = new PageCache();
    // 80 lines of 4 blocks = 320 items per page: three pages fit, a fourth does not.
    for (let n = 0; n < 4; n++) cache.add(page(n * 80, 80, 4));
    expect(cache.items).toBe(1280);
    const dropped = cache.trim(300);
    expect(dropped).toEqual([{ start: 0, end: 80 }]);
    expect(cache.items).toBeLessThanOrEqual(LIMITS.items);
    expect([cache.start, cache.end]).toEqual([80, 320]);
  });

  it('drops newer pages when the reader is up in the history', () => {
    const cache = new PageCache();
    for (let n = 0; n < 4; n++) cache.add(page(n * 80, 80, 4));
    const dropped = cache.trim(10);
    expect(dropped).toEqual([{ start: 240, end: 320 }]);
    expect([cache.start, cache.end]).toEqual([0, 240]);
  });

  it('drops pages when line data passes 8 MiB', () => {
    const big = 'y'.repeat(30_000);
    const cache = new PageCache();
    // 80 lines of 30,000 characters = 2.4 MB per page: three pages are 7.2 MB, a fourth passes 8 MiB.
    for (let n = 0; n < 4; n++) cache.add(page(n * 80, 80, 1, big));
    expect(cache.bytes).toBeGreaterThan(LIMITS.bytes);
    cache.trim(Number.POSITIVE_INFINITY);
    expect(cache.bytes).toBeLessThanOrEqual(LIMITS.bytes);
    expect(cache.start).toBe(80);
  });

  it('never drops the page the reader is on', () => {
    const limits: Limits = { items: 10, bytes: LIMITS.bytes };
    const cache = new PageCache(limits);
    cache.add(page(0, 80, 4));
    cache.add(page(80, 80, 4));
    cache.trim(100);
    expect([cache.start, cache.end]).toEqual([80, 160]);
    cache.trim(100);
    expect(cache.pageCount).toBe(1);
  });

  it('stays within the limits however pages arrive', () => {
    const cache = new PageCache();
    for (let n = 0; n < 60; n++) {
      cache.add(page(n * 80, 80, 3));
      cache.trim(Number.POSITIVE_INFINITY);
      expect(cache.items).toBeLessThanOrEqual(LIMITS.items + 240);
      expect(cache.end).toBe((n + 1) * 80);
    }
    expect(cache.items).toBeLessThanOrEqual(LIMITS.items);
  });
});
