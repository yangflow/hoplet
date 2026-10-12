import type { ChangeStatus, WorkspaceChange } from '@hoplet/shared';

// The workspace inspector's pure parts: the Files tree, the Changes summary, unified diffs and their folds.

/** The letter of a git status, as the tree and the list show it (docs/design.md#color). */
export const STATUS_LETTER: Record<ChangeStatus, string> = {
  modified: 'M',
  added: 'A',
  deleted: 'D',
  untracked: 'U',
};

/** A path's directory and its last segment; the root's files have an empty directory. */
export function splitPath(path: string): { dir: string; name: string } {
  const at = path.lastIndexOf('/');
  return at < 0 ? { dir: '', name: path } : { dir: path.slice(0, at), name: path.slice(at + 1) };
}

export type TreeNode = {
  name: string;
  path: string;
  depth: number;
  /** Folders only: their children, folders first. */
  children?: TreeNode[];
  /** Files only: the path's git status. */
  status?: ChangeStatus;
  /** Folders only: whether a changed path lies below. */
  holdsChanges?: boolean;
};

type Mutable = {
  name: string;
  path: string;
  dir: boolean;
  status?: ChangeStatus;
  children: Map<string, Mutable>;
};

/**
 * The Files tree of the listed paths and the changed ones (a deleted path is no longer in the working tree but
 * is still a change), folders first and each level sorted by name.
 */
export function buildTree(
  paths: readonly string[],
  changes: readonly WorkspaceChange[],
): TreeNode[] {
  const status = new Map(changes.map((c) => [c.path, c.status]));
  const root = new Map<string, Mutable>();
  for (const p of new Set([...paths, ...status.keys()])) {
    const parts = p.split('/').filter(Boolean);
    let level = root;
    let acc = '';
    parts.forEach((part, i) => {
      acc = acc ? `${acc}/${part}` : part;
      const last = i === parts.length - 1;
      let node = level.get(part);
      if (!node) {
        node = { name: part, path: acc, dir: !last, children: new Map() };
        level.set(part, node);
      } else if (!last) node.dir = true;
      if (last && !node.dir) {
        const s = status.get(acc);
        if (s) node.status = s;
      }
      level = node.children;
    });
  }
  return freeze(root, 0);
}

function freeze(map: Map<string, Mutable>, depth: number): TreeNode[] {
  return [...map.values()]
    .sort((a, b) => (a.dir !== b.dir ? (a.dir ? -1 : 1) : a.name.localeCompare(b.name)))
    .map((n) => {
      if (!n.dir) {
        return { name: n.name, path: n.path, depth, ...(n.status ? { status: n.status } : {}) };
      }
      const children = freeze(n.children, depth + 1);
      const holdsChanges = children.some((c) => c.status !== undefined || c.holdsChanges);
      return { name: n.name, path: n.path, depth, children, holdsChanges };
    });
}

/** The rows a tree shows: a folded folder hides what lies below it. */
export function visibleRows(
  nodes: readonly TreeNode[],
  isOpen: (n: TreeNode) => boolean,
): TreeNode[] {
  const out: TreeNode[] = [];
  const walk = (level: readonly TreeNode[]) => {
    for (const n of level) {
      out.push(n);
      if (n.children && isOpen(n)) walk(n.children);
    }
  };
  walk(nodes);
  return out;
}

/** Changes' summary row: how many files, and the lines added and removed across the text ones. */
export function changeSummary(changes: readonly WorkspaceChange[]): {
  files: number;
  added: number;
  removed: number;
} {
  let added = 0;
  let removed = 0;
  for (const c of changes) {
    added += c.added ?? 0;
    removed += c.removed ?? 0;
  }
  return { files: changes.length, added, removed };
}

/** A count as the inspector writes it: 9,214. */
export function formatCount(n: number): string {
  return n.toLocaleString('en-US');
}

/** One row of a diff as the viewer draws it. */
export type DiffLine =
  | { kind: 'hunk'; range: string; context: string }
  | { kind: 'eq'; oldNo: number; newNo: number; text: string }
  | { kind: 'del'; oldNo: number; text: string }
  | { kind: 'add'; newNo: number; text: string }
  /** Unchanged lines between hunks, folded: new-side lines `from`…`from + count - 1`. */
  | { kind: 'fold'; id: number; from: number; count: number }
  | { kind: 'note'; text: string };

type Hunk = { oldStart: number; oldLines: number; newStart: number; newLines: number };

const HUNK = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$/;

/**
 * The rows of a unified diff (hunks only, from the first `@@`), with a fold for the unchanged lines before and
 * between hunks. `\ No newline at end of file` becomes a note row.
 */
export function parseDiff(diff: string): DiffLine[] {
  const out: DiffLine[] = [];
  let oldAt = 0;
  let newAt = 0;
  let prev: Hunk | null = null;
  let folds = 0;
  const lines = diff.split('\n');
  if (lines[lines.length - 1] === '') lines.pop();
  for (const line of lines) {
    const m = HUNK.exec(line);
    if (m) {
      const hunk: Hunk = {
        oldStart: Number(m[1]),
        oldLines: m[2] === undefined ? 1 : Number(m[2]),
        newStart: Number(m[3]),
        newLines: m[4] === undefined ? 1 : Number(m[4]),
      };
      const gapFrom = prev ? prev.newStart + prev.newLines : 1;
      const gap = hunk.newStart - gapFrom;
      // A hunk that adds to an empty side starts at line 0; nothing lies before it.
      if (gap > 0 && hunk.newStart > 0)
        out.push({ kind: 'fold', id: folds++, from: gapFrom, count: gap });
      out.push({
        kind: 'hunk',
        range: `-${m[1]}${m[2] === undefined ? '' : `,${m[2]}`} +${m[3]}${m[4] === undefined ? '' : `,${m[4]}`}`,
        context: m[5] ?? '',
      });
      oldAt = hunk.oldStart;
      newAt = hunk.newStart;
      prev = hunk;
      continue;
    }
    if (!prev) continue;
    const sign = line[0];
    const text = line.slice(1);
    if (sign === '+') out.push({ kind: 'add', newNo: newAt++, text });
    else if (sign === '-') out.push({ kind: 'del', oldNo: oldAt++, text });
    else if (sign === '\\') out.push({ kind: 'note', text: line.slice(2) });
    else out.push({ kind: 'eq', oldNo: oldAt++, newNo: newAt++, text });
  }
  return out;
}

/**
 * The diff with the folds in `open` replaced by the unchanged lines they stand for, taken from the file as it
 * is now (`newLines`, one entry per line). Old-side numbers follow from the shift the hunks before made.
 */
export function unfold(
  rows: readonly DiffLine[],
  open: ReadonlySet<number>,
  newLines: readonly string[],
): DiffLine[] {
  const out: DiffLine[] = [];
  let shift = 0;
  for (const row of rows) {
    if (row.kind === 'add') shift++;
    else if (row.kind === 'del') shift--;
    if (row.kind !== 'fold' || !open.has(row.id)) {
      out.push(row);
      continue;
    }
    for (let n = row.from; n < row.from + row.count; n++) {
      out.push({ kind: 'eq', oldNo: n - shift, newNo: n, text: newLines[n - 1] ?? '' });
    }
  }
  return out;
}

/**
 * The width of a line-number column that holds `largest`: the gutter token, widened by one character per
 * digit beyond three so four- and five-digit numbers never run into the next column.
 */
export function gutterFor(largest: number): string {
  const extra = Math.max(0, String(Math.max(1, largest)).length - 3);
  return extra ? `calc(var(--layout-gutter) + ${extra}ch)` : 'var(--layout-gutter)';
}

/** The largest line number a diff shows on either side. */
export function largestLine(rows: readonly DiffLine[]): number {
  let n = 0;
  for (const r of rows) {
    if (r.kind === 'eq') n = Math.max(n, r.oldNo, r.newNo);
    else if (r.kind === 'del') n = Math.max(n, r.oldNo);
    else if (r.kind === 'add') n = Math.max(n, r.newNo);
  }
  return n;
}
