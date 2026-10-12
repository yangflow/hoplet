/** Layout arithmetic of the virtualized transcript: rows of dynamic height stacked from the top. */

/** The top of every row and, last, the total height. */
export function offsetsOf(heights: readonly number[]): number[] {
  const out = new Array<number>(heights.length + 1);
  let y = 0;
  for (let i = 0; i < heights.length; i++) {
    out[i] = y;
    y += heights[i] ?? 0;
  }
  out[heights.length] = y;
  return out;
}

/** The index of the row that holds `y`; the last row for a `y` past the end. */
export function rowAt(offsets: readonly number[], y: number): number {
  const rows = offsets.length - 1;
  if (rows <= 0) return 0;
  let lo = 0;
  let hi = rows - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if ((offsets[mid] ?? 0) <= y) lo = mid;
    else hi = mid - 1;
  }
  return lo;
}

/** The rows to mount: those in view plus `overscan` pixels either side, as [start, end). */
export function visibleRange(
  offsets: readonly number[],
  top: number,
  height: number,
  overscan: number,
): [number, number] {
  const rows = offsets.length - 1;
  if (rows <= 0) return [0, 0];
  const start = rowAt(offsets, Math.max(0, top - overscan));
  let end = rowAt(offsets, top + height + overscan) + 1;
  end = Math.min(rows, Math.max(end, start + 1));
  return [start, end];
}

/** Where a row was relative to the viewport, to put it back after the layout changed. */
export type Anchor = { key: string; offset: number };

export function anchorAt(
  keys: readonly string[],
  offsets: readonly number[],
  top: number,
): Anchor | null {
  if (keys.length === 0) return null;
  const index = rowAt(offsets, top);
  const key = keys[index];
  return key === undefined ? null : { key, offset: (offsets[index] ?? 0) - top };
}

/** The scroll position that puts the anchored row where it was; null when the row is gone. */
export function restoreScroll(
  keys: readonly string[],
  offsets: readonly number[],
  anchor: Anchor,
): number | null {
  const index = keys.indexOf(anchor.key);
  if (index < 0) return null;
  return (offsets[index] ?? 0) - anchor.offset;
}
