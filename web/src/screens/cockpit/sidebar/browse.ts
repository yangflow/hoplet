/** Paths of the folder browser: breadcrumbs, display, typed paths and keyboard movement. All pure. */

export type Crumb = { label: string; path: string };

/** The browse root shown as `~`. */
export const ROOT_LABEL = '~';

/** The path of `name` inside the directory `base`. */
export function childPath(base: string, name: string): string {
  return base === '/' ? `/${name}` : `${base}/${name}`;
}

function trimSlash(path: string): string {
  return path.length > 1 ? path.replace(/\/+$/, '') || '/' : path;
}

/** The path bar: the root as `~`, then one crumb per directory under it. A path outside the root is one crumb. */
export function breadcrumbs(path: string, root: string): Crumb[] {
  const base = trimSlash(root);
  const target = trimSlash(path);
  const head: Crumb = { label: ROOT_LABEL, path: base };
  if (target === base) return [head];
  const prefix = base === '/' ? '/' : `${base}/`;
  if (!target.startsWith(prefix)) return [{ label: target, path: target }];
  const crumbs = [head];
  let at = base;
  for (const segment of target.slice(prefix.length).split('/')) {
    if (segment === '') continue;
    at = childPath(at, segment);
    crumbs.push({ label: segment, path: at });
  }
  return crumbs;
}

/** `~/github/repopro` for a path under the root, the path itself otherwise. */
export function displayPath(path: string, root: string): string {
  const crumbs = breadcrumbs(path, root);
  if (crumbs.length === 1 && crumbs[0]?.label !== ROOT_LABEL) return path;
  return crumbs.map((c) => c.label).join('/');
}

/** `text` cut to at most `max` characters with an ellipsis in the middle, keeping more of the end. */
export function middleEllipsis(text: string, max: number): string {
  if (text.length <= max || max < 3) return text;
  const keep = max - 1;
  const tail = Math.ceil(keep * 0.6);
  const head = keep - tail;
  return `${text.slice(0, head)}…${text.slice(text.length - tail)}`;
}

/**
 * What a typed path asks the agent for: `~` and `~/x` are under the browse root, an absolute path stays as it
 * is, anything else goes unchanged for the agent to refuse. Null when nothing was typed.
 */
export function resolveTyped(text: string, root: string): string | null {
  const typed = text.trim();
  if (typed === '') return null;
  if (typed === ROOT_LABEL) return ROOT_LABEL;
  if (typed.startsWith('~/')) {
    const rest = typed.slice(2).replace(/\/+$/, '');
    return rest === '' ? ROOT_LABEL : childPath(trimSlash(root), rest);
  }
  return typed.startsWith('/') ? trimSlash(typed) : typed;
}

export type NavKey = 'ArrowDown' | 'ArrowUp' | 'Home' | 'End';

/** The row a navigation key lands on; `index` is -1 while nothing is selected. */
export function moveIndex(index: number, count: number, key: NavKey): number {
  if (count <= 0) return -1;
  switch (key) {
    case 'ArrowDown':
      return Math.min(index + 1, count - 1);
    case 'ArrowUp':
      return index < 0 ? 0 : Math.max(index - 1, 0);
    case 'Home':
      return 0;
    case 'End':
      return count - 1;
  }
}

/** The directory above `path`, or null at the browse root. */
export function parentOf(path: string, root: string): string | null {
  const crumbs = breadcrumbs(path, root);
  return crumbs.length > 1 ? (crumbs[crumbs.length - 2]?.path ?? null) : null;
}
