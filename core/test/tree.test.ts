import { describe, expect, it } from 'vitest';
import { childCounts, descendantsOf, membersOf, parentOf, rootOf } from '../src/control/tree.ts';

const chains = new Map<string, string[]>([
  ['b', ['a']],
  ['c', ['a', 'b']],
  ['d', ['a']],
  ['z', []],
]);

describe('trees of work', () => {
  it('reads the parent and the root from a chain', () => {
    expect(parentOf(chains, 'c')).toBe('b');
    expect(parentOf(chains, 'a')).toBeNull();
    expect(parentOf(chains, 'z')).toBeNull();
    expect(rootOf(chains, 'c')).toBe('a');
    expect(rootOf(chains, 'a')).toBe('a');
  });

  it('counts the children of each session', () => {
    expect(Object.fromEntries(childCounts(chains))).toEqual({ a: 2, b: 1 });
  });

  it('lists descendants in pre-order with their depth', () => {
    expect(descendantsOf(chains, 'a')).toEqual([
      { sessionId: 'b', depth: 1 },
      { sessionId: 'c', depth: 2 },
      { sessionId: 'd', depth: 1 },
    ]);
    expect(descendantsOf(chains, 'b')).toEqual([{ sessionId: 'c', depth: 1 }]);
    expect(descendantsOf(chains, 'd')).toEqual([]);
  });

  it('lists the members below a root', () => {
    expect(membersOf(chains, 'a').sort()).toEqual(['b', 'c', 'd']);
  });
});
