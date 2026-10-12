import type { WorkspaceChange } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  buildTree,
  changeSummary,
  formatCount,
  gutterFor,
  largestLine,
  parseDiff,
  splitPath,
  unfold,
  visibleRows,
} from '../src/screens/workspace/model.ts';
import {
  loadTabs,
  loadWrap,
  saveTabs,
  saveWrap,
  TABS_KEY,
  WRAP_KEY,
} from '../src/screens/workspace/prefs.ts';

const changes: WorkspaceChange[] = [
  { path: 'core/src/store/chunks.ts', status: 'modified', added: 9, removed: 3 },
  { path: 'docs/hosting.md', status: 'modified', added: 9214, removed: 1 },
  { path: 'notes.txt', status: 'untracked', added: 5, removed: 0 },
  { path: 'design/icon.png', status: 'modified', binary: true },
  { path: 'core/src/old.ts', status: 'deleted', added: 0, removed: 12 },
];

function memoryStore(initial: Record<string, string> = {}) {
  const data = new Map(Object.entries(initial));
  return {
    data,
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
  };
}

describe('the Files tree', () => {
  const tree = buildTree(
    ['README.md', 'core/src/index.ts', 'core/src/store/chunks.ts', 'web/index.html', 'notes.txt'],
    changes,
  );

  it('nests path segments, folders first, each level by name', () => {
    expect(tree.map((n) => n.name)).toEqual([
      'core',
      'design',
      'docs',
      'web',
      'notes.txt',
      'README.md',
    ]);
    const src = tree[0]?.children?.[0];
    expect(src?.path).toBe('core/src');
    expect(src?.children?.map((n) => n.name)).toEqual(['store', 'index.ts', 'old.ts']);
    expect(src?.depth).toBe(1);
  });

  it('marks files with their status and folders that hold changes', () => {
    const src = tree[0]?.children?.[0];
    expect(src?.children?.find((n) => n.name === 'old.ts')?.status).toBe('deleted');
    expect(tree.find((n) => n.name === 'notes.txt')?.status).toBe('untracked');
    expect(tree.find((n) => n.name === 'README.md')?.status).toBeUndefined();
    expect(tree[0]?.holdsChanges).toBe(true);
    expect(tree.find((n) => n.name === 'web')?.holdsChanges).toBe(false);
  });

  it('hides what lies below a folded folder', () => {
    const rows = visibleRows(tree, (n) => n.name !== 'core');
    expect(rows.map((n) => n.path)).not.toContain('core/src');
    expect(rows.map((n) => n.path)).toContain('web/index.html');
  });
});

describe('the Changes summary', () => {
  it('counts files and the lines of the text ones', () => {
    expect(changeSummary(changes)).toEqual({ files: 5, added: 9228, removed: 16 });
    expect(formatCount(9228)).toBe('9,228');
  });

  it('splits a path into its directory and name', () => {
    expect(splitPath('core/src/store/chunks.ts')).toEqual({
      dir: 'core/src/store',
      name: 'chunks.ts',
    });
    expect(splitPath('notes.txt')).toEqual({ dir: '', name: 'notes.txt' });
  });
});

const diff = [
  '@@ -1,3 +1,4 @@',
  ' a',
  '+b',
  ' c',
  ' d',
  '@@ -30,3 +31,2 @@ fn',
  ' x',
  '-y',
  ' z',
  '\\ No newline at end of file',
  '',
].join('\n');

describe('a unified diff', () => {
  it('numbers its lines and folds the unchanged ones between hunks', () => {
    const rows = parseDiff(diff);
    expect(rows.map((r) => r.kind)).toEqual([
      'hunk',
      'eq',
      'add',
      'eq',
      'eq',
      'fold',
      'hunk',
      'eq',
      'del',
      'eq',
      'note',
    ]);
    expect(rows[2]).toEqual({ kind: 'add', newNo: 2, text: 'b' });
    expect(rows[5]).toEqual({ kind: 'fold', id: 0, from: 5, count: 26 });
    expect(rows[6]).toEqual({ kind: 'hunk', range: '-30,3 +31,2', context: 'fn' });
    expect(rows[8]).toEqual({ kind: 'del', oldNo: 31, text: 'y' });
    expect(rows[9]).toEqual({ kind: 'eq', oldNo: 32, newNo: 32, text: 'z' });
  });

  it('folds the lines before the first hunk, and nothing before a new file', () => {
    expect(parseDiff('@@ -10,2 +10,2 @@\n a\n b\n')[0]).toEqual({
      kind: 'fold',
      id: 0,
      from: 1,
      count: 9,
    });
    expect(parseDiff('@@ -0,0 +1,2 @@\n+a\n+b\n').map((r) => r.kind)).toEqual([
      'hunk',
      'add',
      'add',
    ]);
  });

  it('opens a fold with the lines of the file as it is now', () => {
    const rows = parseDiff(diff);
    const now = Array.from({ length: 33 }, (_, i) => `line ${i + 1}`);
    const open = unfold(rows, new Set([0]), now);
    expect(open.some((r) => r.kind === 'fold')).toBe(false);
    const first = open.find((r) => r.kind === 'eq' && r.newNo === 5);
    expect(first).toEqual({ kind: 'eq', oldNo: 4, newNo: 5, text: 'line 5' });
    const last = open.find((r) => r.kind === 'eq' && r.newNo === 30);
    expect(last).toEqual({ kind: 'eq', oldNo: 29, newNo: 30, text: 'line 30' });
    expect(unfold(rows, new Set(), now)).toEqual(rows);
  });
});

describe('what the inspector remembers', () => {
  it('keeps the open tabs, in tool order', () => {
    const store = memoryStore();
    expect(loadTabs(store)).toEqual(['changes', 'files']);
    saveTabs(['files'], store);
    expect(store.data.get(TABS_KEY)).toBe('["files"]');
    expect(loadTabs(store)).toEqual(['files']);
    saveTabs(['files', 'changes'], store);
    expect(loadTabs(store)).toEqual(['changes', 'files']);
    saveTabs([], store);
    expect(loadTabs(store)).toEqual([]);
  });

  it('ignores a saved value it cannot read', () => {
    expect(loadTabs(memoryStore({ [TABS_KEY]: '{' }))).toEqual(['changes', 'files']);
    expect(loadTabs(memoryStore({ [TABS_KEY]: '["terminal","files"]' }))).toEqual(['files']);
  });

  it('keeps Wrap', () => {
    const store = memoryStore();
    expect(loadWrap(store)).toBe(false);
    saveWrap(true, store);
    expect(store.data.get(WRAP_KEY)).toBe('1');
    expect(loadWrap(store)).toBe(true);
    saveWrap(false, store);
    expect(loadWrap(store)).toBe(false);
  });
});

describe('the line-number gutter', () => {
  it('widens one character per digit beyond three', () => {
    expect(gutterFor(999)).toBe('var(--layout-gutter)');
    expect(gutterFor(6000)).toBe('calc(var(--layout-gutter) + 1ch)');
    expect(gutterFor(12345)).toBe('calc(var(--layout-gutter) + 2ch)');
  });
  it('takes the largest number of either side of a diff', () => {
    expect(
      largestLine([
        { kind: 'eq', oldNo: 5998, newNo: 5998, text: '' },
        { kind: 'add', newNo: 11003, text: '' },
      ]),
    ).toBe(11003);
  });
});
