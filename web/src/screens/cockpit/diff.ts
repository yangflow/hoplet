export type DiffRow = { kind: 'eq' | 'add' | 'del'; text: string; oldNo?: number; newNo?: number };

export type LineDiff = { added: number; removed: number; rows: DiffRow[]; more: number };

/** Rows shown for one edit before the rest is summarised. */
export const DIFF_ROWS = 80;
const CONTEXT = 2;
/** Largest middle section compared line by line; a longer one is shown as replaced. */
const LCS_LIMIT = 300;

function middleOps(a: string[], b: string[]): ('eq' | 'add' | 'del')[] {
  if (a.length > LCS_LIMIT || b.length > LCS_LIMIT) {
    return [...a.map(() => 'del' as const), ...b.map(() => 'add' as const)];
  }
  const w = b.length + 1;
  const table = new Uint32Array((a.length + 1) * w);
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      table[i * w + j] =
        a[i] === b[j]
          ? (table[(i + 1) * w + j + 1] ?? 0) + 1
          : Math.max(table[(i + 1) * w + j] ?? 0, table[i * w + j + 1] ?? 0);
    }
  }
  const ops: ('eq' | 'add' | 'del')[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      ops.push('eq');
      i++;
      j++;
    } else if ((table[(i + 1) * w + j] ?? 0) >= (table[i * w + j + 1] ?? 0)) {
      ops.push('del');
      i++;
    } else {
      ops.push('add');
      j++;
    }
  }
  while (i++ < a.length) ops.push('del');
  while (j++ < b.length) ops.push('add');
  return ops;
}

/**
 * The lines an edit changed, with two lines of context. Numbers count from the first line of the replaced
 * text, since the stored edit does not say where in the file it applied.
 */
export function lineDiff(before: string, after: string): LineDiff {
  const a = before === '' ? [] : before.split('\n');
  const b = after === '' ? [] : after.split('\n');
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (
    tail < a.length - head &&
    tail < b.length - head &&
    a[a.length - 1 - tail] === b[b.length - 1 - tail]
  ) {
    tail++;
  }
  const ops = middleOps(a.slice(head, a.length - tail), b.slice(head, b.length - tail));
  const rows: DiffRow[] = [];
  for (let n = Math.max(0, head - CONTEXT); n < head; n++) {
    rows.push({ kind: 'eq', text: a[n] ?? '', oldNo: n + 1, newNo: n + 1 });
  }
  let oldAt = head;
  let newAt = head;
  let added = 0;
  let removed = 0;
  for (const op of ops) {
    if (op === 'eq') {
      rows.push({ kind: 'eq', text: a[oldAt] ?? '', oldNo: oldAt + 1, newNo: newAt + 1 });
      oldAt++;
      newAt++;
    } else if (op === 'del') {
      rows.push({ kind: 'del', text: a[oldAt] ?? '', oldNo: oldAt + 1 });
      oldAt++;
      removed++;
    } else {
      rows.push({ kind: 'add', text: b[newAt] ?? '', newNo: newAt + 1 });
      newAt++;
      added++;
    }
  }
  for (let n = 0; n < Math.min(CONTEXT, tail); n++) {
    rows.push({ kind: 'eq', text: a[oldAt + n] ?? '', oldNo: oldAt + n + 1, newNo: newAt + n + 1 });
  }
  return {
    added,
    removed,
    rows: rows.slice(0, DIFF_ROWS),
    more: Math.max(0, rows.length - DIFF_ROWS),
  };
}
