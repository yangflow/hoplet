import type { Entry, LinesPage } from '@hoplet/shared';
import { describe, expect, it, vi } from 'vitest';
import { LIMITS } from '../src/screens/cockpit/page-cache.ts';
import { type Fetcher, SessionReader } from '../src/screens/cockpit/reader.ts';

const SID = '11111111-2222-4333-8444-555555555555';

/** A session of `total` lines, each with `blocks` text blocks; the fetcher serves any range of it. */
function session(total: () => number, blocks = 1) {
  const calls: [number, number][] = [];
  const fetcher: Fetcher = async (start, limit) => {
    calls.push([start, limit]);
    const size = total();
    const from = start < 0 ? Math.max(0, size + start) : Math.min(start, size);
    const to = Math.min(size, from + limit);
    const entries: Entry[] = Array.from({ length: to - from }, (_, i) => ({
      lineNo: from + i,
      role: 'assistant',
      blocks: Array.from({ length: blocks }, () => ({
        type: 'text' as const,
        text: `line ${from + i}`,
      })),
    }));
    return { sessionId: SID, start: from, end: to, total: size, entries } satisfies LinesPage;
  };
  return { fetcher, calls };
}

describe('SessionReader', () => {
  it('opens on the last 80 lines', async () => {
    const { fetcher, calls } = session(() => 1000);
    const reader = new SessionReader(fetcher);
    await reader.open();
    expect(calls).toEqual([[-80, 80]]);
    const snap = reader.getSnapshot();
    expect([snap.start, snap.end, snap.total]).toEqual([920, 1000, 1000]);
    expect(snap.ready && snap.hasOlder && !snap.hasNewer).toBe(true);
  });

  it('opens around a line for a search hit', async () => {
    const { fetcher, calls } = session(() => 1000);
    const reader = new SessionReader(fetcher);
    await reader.open(500);
    expect(calls).toEqual([[460, 80]]);
    const snap = reader.getSnapshot();
    expect([snap.start, snap.end]).toEqual([460, 540]);
    expect(snap.hasNewer).toBe(true);
  });

  it('loads earlier pages one at a time', async () => {
    const { fetcher, calls } = session(() => 1000);
    const reader = new SessionReader(fetcher);
    await reader.open();
    await Promise.all([reader.loadOlder(), reader.loadOlder()]);
    expect(calls.filter(([s]) => s === 840)).toHaveLength(1);
    expect(reader.getSnapshot().start).toBe(840);
  });

  it('keeps the window within 1,000 items while the reader goes up, and fetches the dropped pages again', async () => {
    const { fetcher } = session(() => 5000, 4);
    const reader = new SessionReader(fetcher);
    await reader.open();
    for (let n = 0; n < 12; n++) {
      reader.setFocus(reader.getSnapshot().start);
      await reader.loadOlder();
      const snap = reader.getSnapshot();
      expect(snap.entries.reduce((sum, e) => sum + e.blocks.length, 0)).toBeLessThanOrEqual(
        LIMITS.items + 320,
      );
    }
    const up = reader.getSnapshot();
    expect(up.start).toBeLessThan(4000);
    expect(up.hasNewer).toBe(true);
    expect(up.dropped.length).toBeGreaterThan(0);
    // The reader returns: the pages after the window are fetched again.
    const before = up.end;
    reader.setFocus(before - 1);
    await reader.loadNewer();
    expect(reader.getSnapshot().end).toBe(before + 80);
  });

  it('follows the tail and reads what was appended', async () => {
    let total = 100;
    const { fetcher } = session(() => total);
    const reader = new SessionReader(fetcher);
    await reader.open();
    total = 190;
    await reader.pull();
    const snap = reader.getSnapshot();
    expect(snap.end).toBe(190);
    expect(new Set(snap.entries.map((e) => e.lineNo)).size).toBe(snap.entries.length);
  });

  it('folds calls made while a pull runs into one further round', async () => {
    let total = 100;
    const { fetcher, calls } = session(() => total);
    const reader = new SessionReader(fetcher);
    await reader.open();
    calls.length = 0;
    total = 120;
    const first = reader.pull();
    total = 130;
    const second = reader.pull();
    await Promise.all([first, second]);
    expect(reader.getSnapshot().end).toBe(130);
    expect(calls.every(([start]) => start >= 100)).toBe(true);
  });

  it('reads the tail again instead of paging through a huge gap', async () => {
    let total = 100;
    const { fetcher, calls } = session(() => total);
    const reader = new SessionReader(fetcher);
    await reader.open();
    total = 100_000;
    calls.length = 0;
    await reader.pull();
    // One probe tells the size of the gap; then the tail is read, not 1,250 pages.
    expect(calls).toEqual([
      [100, 80],
      [-80, 80],
    ]);
    expect(reader.getSnapshot().end).toBe(100_000);
  });

  it('ignores a response that arrives after the window was opened again', async () => {
    const waiting: (() => void)[] = [];
    const sizes = [30, 50];
    const slow: Fetcher = async (start, limit) => {
      const size = sizes[waiting.length] ?? 50;
      await new Promise<void>((resolve) => {
        waiting.push(resolve);
      });
      return session(() => size).fetcher(start, limit);
    };
    const reader = new SessionReader(slow);
    const first = reader.open();
    const second = reader.open();
    // The second answer arrives first, the first one later.
    waiting[1]?.();
    await second;
    waiting[0]?.();
    await first;
    expect(reader.getSnapshot().entries).toHaveLength(50);
    expect(reader.getSnapshot().total).toBe(50);
  });

  it('does not ask again at once for a page that failed', async () => {
    const fetcher = vi
      .fn<Fetcher>()
      .mockImplementationOnce((s, l) => session(() => 1000).fetcher(s, l))
      .mockRejectedValue(new Error('down'));
    const reader = new SessionReader(fetcher);
    await reader.open();
    await reader.loadOlder();
    await reader.loadOlder();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(reader.getSnapshot().error).toBe('down');
  });

  it('holds fixed entries without reading', async () => {
    const reader = SessionReader.fixed([
      { lineNo: 18_240, role: 'user', blocks: [{ type: 'text', text: 'hi' }] },
    ]);
    await reader.loadOlder();
    await reader.pull();
    const snap = reader.getSnapshot();
    expect(snap.ready).toBe(true);
    expect(snap.hasOlder || snap.hasNewer).toBe(false);
  });
});
