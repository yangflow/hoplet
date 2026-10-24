import type { Annotation } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  commentCounts,
  extendSelection,
  lineOn,
  pendingOf,
  place,
  rangeOf,
  shown,
  sideOf,
  snippetOf,
  startSelection,
} from '../src/screens/workspace/annotations.ts';
import { type DiffLine, parseDiff } from '../src/screens/workspace/model.ts';

const DIFF = [
  '@@ -1,3 +1,3 @@',
  ' a',
  '-b',
  '+B',
  ' c',
  '@@ -20,3 +20,4 @@',
  ' x',
  '+y',
  ' z',
  ' w',
  '',
].join('\n');
const rows = parseDiff(DIFF);
const row = (pred: (r: DiffLine) => boolean): DiffLine => {
  const r = rows.find(pred);
  if (!r) throw new Error('no row');
  return r;
};

const note = (over: Partial<Annotation>): Annotation => ({
  id: 'aaaaaaaa-0000-4000-8000-000000000001',
  path: 'f.ts',
  side: 'new',
  startLine: 1,
  endLine: 1,
  snippet: 'a',
  body: 'why',
  state: 'pending',
  createdAt: 0,
  updatedAt: 0,
  ...over,
});

describe('lines and sides', () => {
  it('numbers a removed line only at HEAD and an added one only in the working tree', () => {
    const del = row((r) => r.kind === 'del');
    const add = row((r) => r.kind === 'add');
    expect(lineOn(del, 'old')).toBe(2);
    expect(lineOn(del, 'new')).toBeNull();
    expect(lineOn(add, 'new')).toBe(2);
    expect(lineOn(add, 'old')).toBeNull();
    expect(sideOf(del)).toBe('old');
    expect(sideOf(add)).toBe('new');
    expect(sideOf(row((r) => r.kind === 'hunk'))).toBeNull();
  });

  it('picks lines on one side, from either end', () => {
    const start = startSelection(row((r) => r.kind === 'eq' && r.newNo === 22));
    expect(start).toEqual({ side: 'new', anchor: 22, head: 22 });
    if (!start) return;
    const up = extendSelection(
      start,
      row((r) => r.kind === 'add' && r.newNo === 21),
    );
    expect(rangeOf(up)).toEqual({ start: 21, end: 22 });
    // a removed line is not on the working tree's side: the selection stays
    expect(
      extendSelection(
        up,
        row((r) => r.kind === 'del'),
      ),
    ).toBe(up);
    expect(snippetOf(rows, 'new', 20, 22)).toBe('x\ny\nz');
  });
});

describe('place', () => {
  it('keeps a comment on its lines while they hold its text', () => {
    const p = place(rows, note({ startLine: 21, endLine: 22, snippet: 'y\nz' }));
    expect(p.fit).toBe('here');
    expect(rows[p.after]).toMatchObject({ kind: 'eq', newNo: 22 });
  });

  it('follows the text to the nearest lines that hold it', () => {
    const p = place(rows, note({ startLine: 5, endLine: 5, snippet: 'B' }));
    expect(p).toMatchObject({ fit: 'moved', startLine: 2, endLine: 2 });
  });

  it('says the lines changed when the text is gone, and stays by the old numbers', () => {
    const p = place(rows, note({ startLine: 21, endLine: 21, snippet: 'gone' }));
    expect(p.fit).toBe('changed');
    expect(rows[p.after]).toMatchObject({ newNo: 21 });
  });

  it('cannot check lines that are folded or past the last hunk', () => {
    expect(place(rows, note({ startLine: 10, endLine: 12, snippet: 'q' })).fit).toBe('hidden');
    expect(place(rows, note({ startLine: 90, endLine: 90, snippet: 'q' }))).toMatchObject({
      fit: 'hidden',
      after: rows.length - 1,
    });
  });

  it('anchors a removed line at HEAD', () => {
    expect(place(rows, note({ side: 'old', startLine: 2, endLine: 2, snippet: 'b' })).fit).toBe(
      'here',
    );
  });
});

describe('lists', () => {
  const list = [
    note({ id: '1', path: 'a', state: 'pending' }),
    note({ id: '2', path: 'a', state: 'sent' }),
    note({ id: '3', path: 'a', state: 'resolved' }),
    note({ id: '4', path: 'b', state: 'pending' }),
  ];

  it('shows resolved comments only when asked', () => {
    expect(shown(list, 'a', false).map((a) => a.id)).toEqual(['1', '2']);
    expect(shown(list, 'a', true).map((a) => a.id)).toEqual(['1', '2', '3']);
  });

  it('counts the pending comments and their files', () => {
    expect(pendingOf(list)).toEqual({ ids: ['1', '4'], files: 2 });
  });

  it('counts the open and pending comments of each path', () => {
    expect([...commentCounts(list)]).toEqual([
      ['a', { open: 2, pending: 1 }],
      ['b', { open: 1, pending: 1 }],
    ]);
  });
});
