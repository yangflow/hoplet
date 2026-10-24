import type { Annotation, AnnotationSide } from '@hoplet/shared';
import type { DiffLine } from './model.ts';

// Review comments on the lines of a diff (#84), the pure parts: which side a line is on, a selection of lines,
// the snippet they hold, and where a comment sits in the diff as it is now.

/** The number a row has on a side: removed lines only at `HEAD`, added lines only in the working tree. */
export function lineOn(row: DiffLine, side: AnnotationSide): number | null {
  if (row.kind === 'eq') return side === 'old' ? row.oldNo : row.newNo;
  if (row.kind === 'del') return side === 'old' ? row.oldNo : null;
  if (row.kind === 'add') return side === 'new' ? row.newNo : null;
  return null;
}

/** The side a comment that starts on this row is on: a removed line's is `HEAD`, any other's the working tree. */
export function sideOf(row: DiffLine): AnnotationSide | null {
  if (row.kind === 'del') return 'old';
  if (row.kind === 'add' || row.kind === 'eq') return 'new';
  return null;
}

/** Lines picked in the diff: where the press started (`anchor`) and where it is now (`head`), on one side. */
export type Selection = { side: AnnotationSide; anchor: number; head: number };

/** A selection that starts on a row; null on a row that is not a line. */
export function startSelection(row: DiffLine): Selection | null {
  const side = sideOf(row);
  const n = side ? lineOn(row, side) : null;
  return side && n !== null ? { side, anchor: n, head: n } : null;
}

/** The selection stretched to a row (a drag or a shift-click); a row not on its side leaves it as it is. */
export function extendSelection(sel: Selection, row: DiffLine): Selection {
  const n = lineOn(row, sel.side);
  return n === null ? sel : { ...sel, head: n };
}

/** The first and last line of a selection. */
export function rangeOf(sel: Selection): { start: number; end: number } {
  return { start: Math.min(sel.anchor, sel.head), end: Math.max(sel.anchor, sel.head) };
}

/** The text of the lines `start`…`end` on a side, as the diff shows them, one per line. */
export function snippetOf(
  rows: readonly DiffLine[],
  side: AnnotationSide,
  start: number,
  end: number,
): string {
  const out: string[] = [];
  for (const row of rows) {
    const n = lineOn(row, side);
    if (n !== null && n >= start && n <= end && row.kind !== 'hunk') out.push(textOf(row));
  }
  return out.join('\n');
}

function textOf(row: DiffLine): string {
  return row.kind === 'eq' || row.kind === 'add' || row.kind === 'del' ? row.text : '';
}

/**
 * Where a comment sits in the diff as it is now:
 * - `here`: its lines still hold the text it was written on;
 * - `moved`: that text is now at other line numbers, which it takes;
 * - `hidden`: its lines are not shown (folded, or past the last hunk), so they cannot be checked;
 * - `changed`: the text is gone; it stays by its old line numbers and shows the text as written.
 * The card goes after row `after`; -1 puts it before the first row.
 */
export type Placement = {
  after: number;
  startLine: number;
  endLine: number;
  fit: 'here' | 'moved' | 'hidden' | 'changed';
};

export function place(rows: readonly DiffLine[], a: Annotation): Placement {
  const lines: { at: number; no: number; text: string }[] = [];
  rows.forEach((row, at) => {
    const no = lineOn(row, a.side);
    if (no !== null) lines.push({ at, no, text: textOf(row) });
  });
  const want = a.snippet.split('\n');
  const count = a.endLine - a.startLine + 1;
  const matchesAt = (i: number) =>
    want.every((text, k) => {
      const line = lines[i + k];
      const first = lines[i];
      return (
        line !== undefined && first !== undefined && line.text === text && line.no === first.no + k
      );
    });

  if (want.length === count) {
    const i = lines.findIndex((l) => l.no === a.startLine);
    if (i >= 0 && matchesAt(i)) {
      return {
        after: lines[i + count - 1]?.at ?? -1,
        startLine: a.startLine,
        endLine: a.endLine,
        fit: 'here',
      };
    }
    // The nearest run of lines that holds the text.
    let best = -1;
    for (let i = 0; i + want.length <= lines.length; i++) {
      if (!matchesAt(i)) continue;
      const di = Math.abs((lines[i]?.no ?? 0) - a.startLine);
      if (best < 0 || di < Math.abs((lines[best]?.no ?? 0) - a.startLine)) best = i;
    }
    const first = lines[best];
    const last = lines[best + want.length - 1];
    if (first && last) {
      return { after: last.at, startLine: first.no, endLine: last.no, fit: 'moved' };
    }
  }

  const fold = foldHolding(rows, a);
  if (fold !== null)
    return { after: fold, startLine: a.startLine, endLine: a.endLine, fit: 'hidden' };
  const lastShown = lines[lines.length - 1];
  if (lastShown && a.startLine > lastShown.no) {
    return { after: rows.length - 1, startLine: a.startLine, endLine: a.endLine, fit: 'hidden' };
  }
  let after = -1;
  for (const l of lines) if (l.no <= a.endLine) after = l.at;
  return { after, startLine: a.startLine, endLine: a.endLine, fit: 'changed' };
}

/** The index of the fold that hides all of a comment's lines on its side, or null. */
function foldHolding(rows: readonly DiffLine[], a: Annotation): number | null {
  // A fold's lines are numbered in the working tree; at HEAD they are shifted by the lines added and removed before.
  let shift = 0;
  for (let i = 0; i < rows.length; i++) {
    const row = rows[i];
    if (!row) continue;
    if (row.kind === 'add') shift++;
    else if (row.kind === 'del') shift--;
    else if (row.kind === 'fold') {
      const from = a.side === 'new' ? row.from : row.from - shift;
      if (a.startLine >= from && a.endLine <= from + row.count - 1) return i;
    }
  }
  return null;
}

/** The comments a file shows: pending and sent ones, resolved ones only when asked. */
export function shown(list: readonly Annotation[], path: string, resolved: boolean): Annotation[] {
  return list.filter((a) => a.path === path && (resolved || a.state !== 'resolved'));
}

/** The pending comments and how many files they are in. */
export function pendingOf(list: readonly Annotation[]): { ids: string[]; files: number } {
  const pending = list.filter((a) => a.state === 'pending');
  return { ids: pending.map((a) => a.id), files: new Set(pending.map((a) => a.path)).size };
}

/** Per path, the comments not resolved and how many of them are pending: the Changes list's counts. */
export function commentCounts(
  list: readonly Annotation[],
): ReadonlyMap<string, { open: number; pending: number }> {
  const out = new Map<string, { open: number; pending: number }>();
  for (const a of list) {
    if (a.state === 'resolved') continue;
    const c = out.get(a.path) ?? { open: 0, pending: 0 };
    out.set(a.path, { open: c.open + 1, pending: c.pending + (a.state === 'pending' ? 1 : 0) });
  }
  return out;
}
