import { describe, expect, it } from 'vitest';
import {
  breadcrumbs,
  childPath,
  displayPath,
  middleEllipsis,
  moveIndex,
  parentOf,
  resolveTyped,
} from '../src/screens/cockpit/sidebar/browse.ts';

const ROOT = '/Users/mark';

describe('breadcrumbs', () => {
  it('shows the browse root as ~ with one crumb per directory under it', () => {
    expect(breadcrumbs('/Users/mark/github/repopro', ROOT)).toEqual([
      { label: '~', path: '/Users/mark' },
      { label: 'github', path: '/Users/mark/github' },
      { label: 'repopro', path: '/Users/mark/github/repopro' },
    ]);
  });

  it('is just ~ at the root, also with trailing slashes', () => {
    expect(breadcrumbs('/Users/mark', ROOT)).toEqual([{ label: '~', path: ROOT }]);
    expect(breadcrumbs('/Users/mark/', '/Users/mark/')).toEqual([{ label: '~', path: ROOT }]);
  });

  it('works under a root of /', () => {
    expect(breadcrumbs('/srv/app', '/')).toEqual([
      { label: '~', path: '/' },
      { label: 'srv', path: '/srv' },
      { label: 'app', path: '/srv/app' },
    ]);
  });

  it('keeps a path outside the root whole, and does not mistake a sibling prefix for a child', () => {
    expect(breadcrumbs('/etc/ssh', ROOT)).toEqual([{ label: '/etc/ssh', path: '/etc/ssh' }]);
    expect(breadcrumbs('/Users/mark2/x', ROOT)).toHaveLength(1);
  });

  it('finds the parent, none at the root', () => {
    expect(parentOf('/Users/mark/github/repopro', ROOT)).toBe('/Users/mark/github');
    expect(parentOf('/Users/mark/github', ROOT)).toBe(ROOT);
    expect(parentOf(ROOT, ROOT)).toBeNull();
  });
});

describe('path display', () => {
  it('writes the root as ~', () => {
    expect(displayPath('/Users/mark/github/repopro', ROOT)).toBe('~/github/repopro');
    expect(displayPath(ROOT, ROOT)).toBe('~');
    expect(displayPath('/etc/ssh', ROOT)).toBe('/etc/ssh');
  });

  it('cuts the middle of a long path and keeps its end', () => {
    const long = '/Users/mark/github/repopro/hoplet/web/src/screens/cockpit/add-project.tsx';
    const cut = middleEllipsis(long, 40);
    expect(cut).toHaveLength(40);
    expect(cut.startsWith('/Users/mark')).toBe(true);
    expect(cut.endsWith('cockpit/add-project.tsx')).toBe(true);
    expect(cut).toContain('…');
  });

  it('leaves a short path alone', () => {
    expect(middleEllipsis('/Users/mark', 40)).toBe('/Users/mark');
  });

  it('joins a child onto a directory', () => {
    expect(childPath('/', 'srv')).toBe('/srv');
    expect(childPath('/srv', 'app')).toBe('/srv/app');
  });
});

describe('typed paths', () => {
  it('resolves ~ and ~/x under the root and keeps absolute paths', () => {
    expect(resolveTyped('~', ROOT)).toBe('~');
    expect(resolveTyped('  ~/github/repopro/ ', ROOT)).toBe('/Users/mark/github/repopro');
    expect(resolveTyped('~/', ROOT)).toBe('~');
    expect(resolveTyped('/etc/ssh/', ROOT)).toBe('/etc/ssh');
  });

  it('passes a relative path through for the agent to refuse, and nothing for nothing', () => {
    expect(resolveTyped('github', ROOT)).toBe('github');
    expect(resolveTyped('   ', ROOT)).toBeNull();
  });
});

describe('keyboard movement', () => {
  it('moves down and up within the rows', () => {
    expect(moveIndex(-1, 4, 'ArrowDown')).toBe(0);
    expect(moveIndex(0, 4, 'ArrowDown')).toBe(1);
    expect(moveIndex(3, 4, 'ArrowDown')).toBe(3);
    expect(moveIndex(2, 4, 'ArrowUp')).toBe(1);
    expect(moveIndex(0, 4, 'ArrowUp')).toBe(0);
    expect(moveIndex(-1, 4, 'ArrowUp')).toBe(0);
  });

  it('jumps to the ends and ignores an empty list', () => {
    expect(moveIndex(2, 4, 'Home')).toBe(0);
    expect(moveIndex(1, 4, 'End')).toBe(3);
    expect(moveIndex(-1, 0, 'ArrowDown')).toBe(-1);
  });
});
