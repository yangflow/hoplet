/**
 * Reads a session as a bounded window of pages: the last lines first, earlier pages on request, newer pages
 * when the window left the tail. It never holds a whole session (docs/architecture.md).
 */
import type { Entry, LinesPage } from '@hoplet/shared';
import { LIMITS, type Limits, PAGE_LINES, PageCache, pageOf } from './page-cache.ts';

export type Fetcher = (start: number, limit: number) => Promise<LinesPage>;

export type ReaderSnapshot = {
  entries: Entry[];
  /** The lines the window covers: [start, end). */
  start: number;
  end: number;
  /** The session's line count as last learned. */
  total: number;
  /** The first page arrived. */
  ready: boolean;
  hasOlder: boolean;
  hasNewer: boolean;
  loading: 'open' | 'older' | 'newer' | null;
  error: string | null;
  /** Lines dropped from the window since it was opened, newest drop last. */
  dropped: { start: number; end: number }[];
};

/** A gap larger than this is not paged through while following the tail; the tail is read again. */
/** How long a page that failed to load waits before the reader asks again. */
const RETRY_MS = 3000;
const MAX_CATCH_UP_LINES = PAGE_LINES * 12;

export class SessionReader {
  private readonly fetcher: Fetcher;
  private readonly cache: PageCache;
  private readonly listeners = new Set<() => void>();
  private generation = 0;
  private total = 0;
  private ready = false;
  private focus = Number.POSITIVE_INFINITY;
  private error: string | null = null;
  private loading = new Set<'open' | 'older' | 'newer'>();
  private dropped: { start: number; end: number }[] = [];
  private view: ReaderSnapshot | null = null;
  private pulling: Promise<void> | null = null;
  private pullAgain = false;
  /** Holds fixed entries and never reads: development fixtures. */
  private fixed = false;
  /** A failed page is not asked for again before this time. */
  private retryAt = { older: 0, newer: 0 };

  constructor(fetcher: Fetcher, limits: Limits = LIMITS) {
    this.fetcher = fetcher;
    this.cache = new PageCache(limits);
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  getSnapshot = (): ReaderSnapshot => {
    if (this.view) return this.view;
    const loading = this.loading.has('open')
      ? 'open'
      : this.loading.has('older')
        ? 'older'
        : this.loading.has('newer')
          ? 'newer'
          : null;
    this.view = {
      entries: this.cache.entries(),
      start: this.cache.start,
      end: this.cache.end,
      total: this.total,
      ready: this.ready,
      hasOlder: !this.fixed && !this.cache.empty && this.cache.start > 0,
      hasNewer: !this.fixed && !this.cache.empty && this.cache.end < this.total,
      loading,
      error: this.error,
      dropped: this.dropped,
    };
    return this.view;
  };

  private changed(): void {
    this.view = null;
    for (const listener of [...this.listeners]) listener();
  }

  /** Where the reader is: the line whose page must stay when the window is trimmed. */
  setFocus(line: number): void {
    this.focus = line;
  }

  /** Learns that the session has this many lines. */
  noteTotal(total: number): void {
    if (total <= this.total) return;
    this.total = total;
    this.changed();
  }

  private add(page: LinesPage, generation: number): boolean {
    if (generation !== this.generation) return false;
    this.total = Math.max(this.total, page.total);
    const added = this.cache.add(pageOf(page.start, page.end, page.entries));
    if (added) {
      const dropped = this.cache.trim(this.focus);
      if (dropped.length > 0) this.dropped = [...this.dropped, ...dropped];
    }
    return added;
  }

  private fail(generation: number, e: unknown): void {
    if (generation === this.generation) this.error = e instanceof Error ? e.message : String(e);
  }

  /** Opens the window on the last lines, or around a line. */
  async open(around: number | null = null): Promise<void> {
    if (this.fixed) return;
    const generation = ++this.generation;
    this.cache.clear();
    this.dropped = [];
    this.ready = false;
    this.error = null;
    this.total = 0;
    this.pulling = null;
    this.loading = new Set(['open']);
    this.focus = around ?? Number.POSITIVE_INFINITY;
    this.changed();
    try {
      const start = around === null ? -PAGE_LINES : Math.max(0, around - PAGE_LINES / 2);
      const page = await this.fetcher(start, PAGE_LINES);
      if (generation !== this.generation) return;
      this.cache.clear();
      this.add(page, generation);
      this.ready = true;
    } catch (e) {
      this.fail(generation, e);
    } finally {
      if (generation === this.generation) {
        this.loading.delete('open');
        this.changed();
      }
    }
  }

  /** Reads the page before the window. */
  async loadOlder(): Promise<void> {
    if (
      Date.now() < this.retryAt.older ||
      this.fixed ||
      !this.ready ||
      this.loading.has('older') ||
      this.cache.empty ||
      this.cache.start === 0
    )
      return;
    const generation = this.generation;
    const end = this.cache.start;
    const start = Math.max(0, end - PAGE_LINES);
    this.loading.add('older');
    this.changed();
    try {
      this.add(await this.fetcher(start, end - start), generation);
      this.error = null;
    } catch (e) {
      this.retryAt.older = Date.now() + RETRY_MS;
      this.fail(generation, e);
    } finally {
      if (generation === this.generation) {
        this.loading.delete('older');
        this.changed();
      }
    }
  }

  /** Reads the page after the window, when the window left the tail. */
  async loadNewer(): Promise<void> {
    if (
      Date.now() < this.retryAt.newer ||
      this.fixed ||
      !this.ready ||
      this.loading.has('newer') ||
      this.cache.empty ||
      this.cache.end >= this.total
    )
      return;
    const generation = this.generation;
    this.loading.add('newer');
    this.changed();
    try {
      this.add(await this.fetcher(this.cache.end, PAGE_LINES), generation);
      this.error = null;
    } catch (e) {
      this.retryAt.newer = Date.now() + RETRY_MS;
      this.fail(generation, e);
    } finally {
      if (generation === this.generation) {
        this.loading.delete('newer');
        this.changed();
      }
    }
  }

  /**
   * Follows the tail: reads whatever the session gained since the window's end. Calls while one runs are
   * folded into a further round, so lines are read once and in order.
   */
  pull(): Promise<void> {
    if (this.fixed) return Promise.resolve();
    if (this.pulling) {
      this.pullAgain = true;
      return this.pulling;
    }
    const generation = this.generation;
    const run = async () => {
      try {
        do {
          this.pullAgain = false;
          if (!this.ready || this.cache.empty) return;
          for (;;) {
            // A gap too big to page through is not read: the window moves to the tail.
            if (this.total - this.cache.end > MAX_CATCH_UP_LINES) {
              const tail = await this.fetcher(-PAGE_LINES, PAGE_LINES);
              if (generation !== this.generation) return;
              this.cache.clear();
              this.total = Math.max(this.total, tail.total);
              this.add(tail, generation);
              this.changed();
              break;
            }
            const at = this.cache.end;
            const page = await this.fetcher(at, PAGE_LINES);
            if (generation !== this.generation) return;
            const grew = this.add(page, generation) && page.end > at;
            this.error = null;
            this.changed();
            if (!grew || page.end >= page.total) break;
          }
        } while (this.pullAgain);
      } catch (e) {
        this.fail(generation, e);
        this.changed();
      }
    };
    this.pulling = run().finally(() => {
      if (generation === this.generation) this.pulling = null;
    });
    return this.pulling;
  }

  /** A reader that holds the given entries and never fetches: development fixtures. */
  static fixed(entries: Entry[], total = entries.length): SessionReader {
    const reader = new SessionReader(() => Promise.reject(new Error('fixture')));
    const first = entries[0]?.lineNo ?? 0;
    const end = (entries[entries.length - 1]?.lineNo ?? -1) + 1;
    reader.cache.add(pageOf(first, end, entries));
    reader.total = Math.max(total, end);
    reader.ready = true;
    reader.fixed = true;
    return reader;
  }

  /** Reads the last lines again: the window moves back to the tail. */
  async jumpToTail(): Promise<void> {
    await this.open(null);
  }
}
