/**
 * The pages of a session a window keeps (docs/spec.md#reading-a-session): contiguous lines, at most 1,000
 * items and 8 MiB of line data. Pages that leave the limits are dropped from the end farther from where the
 * reader is, and fetched again when the reader returns.
 */
import type { Entry } from '@hoplet/shared';

export type Limits = { items: number; bytes: number };

export const LIMITS: Limits = { items: 1000, bytes: 8 * 1024 * 1024 };
/** Lines per request. */
export const PAGE_LINES = 80;

/** What an entry adds to the window: one item per block, at least one. */
export function itemsOf(entry: Entry): number {
  return Math.max(1, entry.blocks.length);
}

/** The line data an entry stands for: the stored size of an elided block, the text length of the rest. */
export function bytesOf(entry: Entry): number {
  let n = 0;
  for (const block of entry.blocks) {
    if ('elided' in block) n += block.bytes;
    else if (block.type === 'text' || block.type === 'thinking') n += block.text.length;
    else if (block.type === 'tool-result') n += block.content.length;
    else if (block.type === 'tool-call')
      n += JSON.stringify(block.input ?? null).length + block.name.length;
    else if (block.type === 'image') n += (block.data?.length ?? 0) + (block.url?.length ?? 0);
    else n += (block.text?.length ?? 0) + (block.label?.length ?? 0);
  }
  return n + 64;
}

export type Page = { start: number; end: number; entries: Entry[]; items: number; bytes: number };

export function pageOf(start: number, end: number, entries: Entry[]): Page {
  return {
    start,
    end,
    entries,
    items: entries.reduce((n, e) => n + itemsOf(e), 0),
    bytes: entries.reduce((n, e) => n + bytesOf(e), 0),
  };
}

export class PageCache {
  private pages: Page[] = [];
  private readonly limits: Limits;

  constructor(limits: Limits = LIMITS) {
    this.limits = limits;
  }

  get start(): number {
    return this.pages[0]?.start ?? 0;
  }

  get end(): number {
    return this.pages[this.pages.length - 1]?.end ?? 0;
  }

  get empty(): boolean {
    return this.pages.length === 0;
  }

  get items(): number {
    return this.pages.reduce((n, p) => n + p.items, 0);
  }

  get bytes(): number {
    return this.pages.reduce((n, p) => n + p.bytes, 0);
  }

  get pageCount(): number {
    return this.pages.length;
  }

  clear(): void {
    this.pages = [];
  }

  entries(): Entry[] {
    return this.pages.flatMap((p) => p.entries);
  }

  /**
   * Adds a page that continues the window at either end, or replaces lines it already holds; returns false,
   * and keeps nothing, when it neither touches nor overlaps the window.
   */
  add(page: Page): boolean {
    if (this.empty) {
      this.pages = [page];
      return true;
    }
    if (page.end < this.start || page.start > this.end) return false;
    const kept = this.pages.filter((p) => p.end <= page.start || p.start >= page.end);
    // A page that cuts into one we keep would leave a hole or a repeat; take the new lines whole.
    const before = this.pages.filter((p) => p.start < page.start && p.end > page.start);
    const after = this.pages.filter((p) => p.start < page.end && p.end > page.end);
    if (before.length > 0 || after.length > 0) {
      const lo = Math.min(page.start, ...before.map((p) => p.start));
      const hi = Math.max(page.end, ...after.map((p) => p.end));
      const merged = new Map<number, Entry>();
      for (const p of [...before, ...after]) for (const e of p.entries) merged.set(e.lineNo, e);
      for (const e of page.entries) merged.set(e.lineNo, e);
      const entries = [...merged.values()].sort((a, b) => a.lineNo - b.lineNo);
      this.pages = [...kept, pageOf(lo, hi, entries)].sort((a, b) => a.start - b.start);
    } else {
      this.pages = [...kept, page].sort((a, b) => a.start - b.start);
    }
    return true;
  }

  /**
   * Drops pages until the window is within its limits. The page holding `focus` stays; of the two ends, the
   * one farther from it goes first. Returns the dropped line ranges.
   */
  trim(focus: number): { start: number; end: number }[] {
    const dropped: { start: number; end: number }[] = [];
    while (
      this.pages.length > 1 &&
      (this.items > this.limits.items || this.bytes > this.limits.bytes)
    ) {
      const first = this.pages[0] as Page;
      const last = this.pages[this.pages.length - 1] as Page;
      const holdsFocus = (p: Page) => focus >= p.start && focus < p.end;
      const fromStart = holdsFocus(last)
        ? true
        : holdsFocus(first)
          ? false
          : focus - first.end >= last.start - focus;
      const page = fromStart ? first : last;
      this.pages = fromStart ? this.pages.slice(1) : this.pages.slice(0, -1);
      dropped.push({ start: page.start, end: page.end });
    }
    return dropped;
  }
}
