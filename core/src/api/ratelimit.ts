/** A fixed-window counter per key, in memory: the limit of the routes that need no host token (ADR 0021). */
export interface RateLimiter {
  /** Counts one request of `key`; false when it is over the limit. */
  allow(key: string): boolean;
}

export function createRateLimiter(
  limit: number,
  windowMs: number,
  now: () => number = Date.now,
): RateLimiter {
  const windows = new Map<string, { start: number; count: number }>();
  return {
    allow(key) {
      const at = now();
      if (windows.size > 10_000) {
        for (const [k, w] of windows) if (at - w.start >= windowMs) windows.delete(k);
      }
      const w = windows.get(key);
      if (!w || at - w.start >= windowMs) {
        windows.set(key, { start: at, count: 1 });
        return true;
      }
      w.count += 1;
      return w.count <= limit;
    },
  };
}
