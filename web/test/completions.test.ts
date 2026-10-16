import type { Command } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  anchorX,
  filterCommands,
  filterPaths,
  highlight,
  insertPick,
  keyAction,
  matchAt,
  moveSelection,
  triggerAt,
} from '../src/screens/cockpit/composer/completions.ts';

const cmd = (name: string, kind: Command['kind'] = 'command'): Command => ({
  name,
  description: '',
  kind,
  source: 'builtin',
});

describe('triggerAt', () => {
  it('opens commands only at the very start', () => {
    expect(triggerAt('/co', 3)).toEqual({ kind: 'slash', query: 'co', start: 0, end: 3 });
    expect(triggerAt('a /co', 5)).toBeNull();
    expect(triggerAt('/co x', 5)).toBeNull();
  });
  it('opens paths at the start or after whitespace', () => {
    expect(triggerAt('@src', 4)).toEqual({ kind: 'at', query: 'src', start: 0, end: 4 });
    expect(triggerAt('see @a/b', 8)).toEqual({ kind: 'at', query: 'a/b', start: 4, end: 8 });
    expect(triggerAt('mail@x', 6)).toBeNull();
  });
  it('reads only the text before the caret', () => {
    expect(triggerAt('/compact', 2)?.query).toBe('c');
  });
});

describe('matching', () => {
  it('matches a prefix or a segment after : or -', () => {
    expect(matchAt('compact', 'co')).toBe(0);
    expect(matchAt('dodo:checkout', 'ch')).toBe(5);
    expect(matchAt('compact', 'pa')).toBe(-1);
  });
  it('matches paths anywhere', () => {
    expect(matchAt('core/src/index.ts', 'src', true)).toBe(5);
  });
  it('splits the name around the hit', () => {
    expect(highlight('compact', 'co')).toEqual([
      { text: 'co', hit: true },
      { text: 'mpact', hit: false },
    ]);
    expect(highlight('compact', '')).toEqual([{ text: 'compact', hit: false }]);
  });
});

describe('filters', () => {
  it('lists commands before skills, keeping the reported order', () => {
    const all = [cmd('b-skill', 'skill'), cmd('b'), cmd('a')];
    expect(filterCommands(all, '').map((i) => i.name)).toEqual(['b', 'a', 'b-skill']);
    expect(filterCommands(all, 'b').map((i) => i.name)).toEqual(['b', 'b-skill']);
  });
  it('lists folders, then files, shallow first', () => {
    const items = filterPaths(['a/b/c.ts', 'a/d.ts', 'e.ts'], '');
    expect(items.map((i) => i.path)).toEqual(['a/', 'a/b/', 'e.ts', 'a/d.ts', 'a/b/c.ts']);
    expect(items[0]?.dir).toBe(true);
    expect(filterPaths(['a/d.ts', 'e.ts'], 'zq')).toEqual([]);
  });
  it('caps the list', () => {
    const many = Array.from({ length: 80 }, (_, i) => `f${i}.ts`);
    expect(filterPaths(many, '')).toHaveLength(50);
  });
});

describe('insertPick', () => {
  it('replaces the token and puts the caret after the space', () => {
    const t = triggerAt('/co', 3);
    if (!t) throw new Error('no trigger');
    expect(
      insertPick('/co', t, { kind: 'slash', command: cmd('compact'), name: 'compact' }),
    ).toEqual({
      text: '/compact ',
      caret: 9,
    });
  });
  it('keeps the text after the caret without doubling the space', () => {
    const text = 'see @a rest';
    const t = triggerAt(text, 6);
    if (!t) throw new Error('no trigger');
    expect(insertPick(text, t, { kind: 'at', path: 'a/b.ts', dir: false })).toEqual({
      text: 'see @a/b.ts rest',
      caret: 12,
    });
  });
});

describe('keys', () => {
  it('maps the list keys', () => {
    expect(keyAction('ArrowDown', false, false)).toBe('down');
    expect(keyAction('ArrowUp', false, false)).toBe('up');
    expect(keyAction('Tab', false, false)).toBe('insert');
    expect(keyAction('Enter', false, false)).toBe('insert');
    expect(keyAction('Escape', false, false)).toBe('close');
    expect(keyAction('a', false, false)).toBeNull();
  });
  it('leaves Shift+Enter and Shift+Tab alone and ignores IME composition', () => {
    expect(keyAction('Enter', true, false)).toBeNull();
    expect(keyAction('Tab', true, false)).toBeNull();
    expect(keyAction('Enter', false, true)).toBeNull();
  });
  it('wraps the selection', () => {
    expect(moveSelection(2, 3, 'down')).toBe(0);
    expect(moveSelection(0, 3, 'up')).toBe(2);
    expect(moveSelection(0, 0, 'down')).toBe(0);
  });
});

describe('anchorX', () => {
  it('follows the caret and stays inside the composer', () => {
    expect(anchorX(100, 900, 620)).toBe(92);
    expect(anchorX(2, 900, 620)).toBe(0);
    expect(anchorX(700, 900, 620)).toBe(280);
    expect(anchorX(100, 500, 620)).toBe(0);
  });
});
