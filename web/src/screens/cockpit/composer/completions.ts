import type { Command } from '@hoplet/shared';
import { t } from '../../../lib/i18n.ts';

/** What the text before the caret asks to complete: a command at the very start, or a path after whitespace. */
export type Trigger = {
  kind: 'slash' | 'at';
  /** What follows the `/` or `@`, up to the caret. */
  query: string;
  /** Where the `/` or `@` stands. */
  start: number;
  /** The caret: the text from `start` to here is replaced on insertion. */
  end: number;
};

/**
 * `/` only at the very start of the message opens commands; `@` at the start or after whitespace opens paths.
 * The token ends at the caret and holds no whitespace.
 */
export function triggerAt(text: string, caret: number): Trigger | null {
  const head = text.slice(0, caret);
  if (head.startsWith('/') && !/\s/.test(head)) {
    return { kind: 'slash', query: head.slice(1), start: 0, end: caret };
  }
  const at = head.search(/(?:^|\s)@[^\s@]*$/);
  if (at < 0) return null;
  const start = head.indexOf('@', at);
  return { kind: 'at', query: head.slice(start + 1), start, end: caret };
}

/**
 * Where the query matches the name: a prefix of the name or of a segment after `:` or `-` for commands, any
 * substring for paths. -1 for no match.
 */
export function matchAt(name: string, query: string, path = false): number {
  const lower = name.toLowerCase();
  const q = query.toLowerCase();
  if (path) return lower.indexOf(q);
  if (lower.startsWith(q)) return 0;
  for (const m of lower.matchAll(/[:-]/g)) {
    if (lower.startsWith(q, (m.index ?? 0) + 1)) return (m.index ?? 0) + 1;
  }
  return -1;
}

export type Part = { text: string; hit: boolean };

/** The name split around the matched run, which the list draws in the accent colour. */
export function highlight(name: string, query: string, path = false): Part[] {
  const at = query ? matchAt(name, query, path) : -1;
  if (at < 0) return [{ text: name, hit: false }];
  return [
    { text: name.slice(0, at), hit: false },
    { text: name.slice(at, at + query.length), hit: true },
    { text: name.slice(at + query.length), hit: false },
  ].filter((p) => p.text !== '');
}

/** One row of the command list. */
export type CommandItem = { kind: 'slash'; command: Command; name: string };
/** One row of the path list. */
export type PathItem = { kind: 'at'; path: string; dir: boolean };
export type Item = CommandItem | PathItem;

/** The commands that match, Commands before Skills, each group in the order the session reported. */
export function filterCommands(commands: readonly Command[], query: string): CommandItem[] {
  const hits = commands.filter((c) => !query || matchAt(c.name, query) >= 0);
  return [
    ...hits.filter((c) => c.kind === 'command'),
    ...hits.filter((c) => c.kind === 'skill'),
  ].map((command) => ({ kind: 'slash', command, name: command.name }));
}

/** The most paths one list offers. */
export const MAX_PATHS = 50;

/** The folders the files stand in, each once, with a trailing slash. */
export function foldersOf(paths: readonly string[]): string[] {
  const seen = new Set<string>();
  for (const p of paths) {
    const parts = p.split('/');
    for (let i = 1; i < parts.length; i++) seen.add(`${parts.slice(0, i).join('/')}/`);
  }
  return [...seen].sort();
}

/** The folders, then the files, whose path holds the query; shallow paths first, at most `MAX_PATHS`. */
export function filterPaths(paths: readonly string[], query: string): PathItem[] {
  const q = query.toLowerCase();
  const depth = (p: string) => p.replace(/\/$/, '').split('/').length;
  const keep = (list: readonly string[], dir: boolean) =>
    list
      .map((path, i) => ({ path, dir, i }))
      .filter((x) => q === '' || x.path.toLowerCase().includes(q))
      .sort((a, b) => depth(a.path) - depth(b.path) || a.i - b.i);
  return [...keep(foldersOf(paths), true), ...keep(paths, false)]
    .slice(0, MAX_PATHS)
    .map(({ path, dir }) => ({ kind: 'at', path, dir }));
}

/** The text with the trigger's token replaced by the pick and a space; the caret goes after the space. */
export function insertPick(
  text: string,
  trigger: Trigger,
  item: Item,
): { text: string; caret: number } {
  const word = item.kind === 'slash' ? `/${item.name}` : `@${item.path}`;
  const after = text.slice(trigger.end);
  const insert = `${word} `;
  const rest = after.startsWith(' ') ? after.slice(1) : after;
  return {
    text: text.slice(0, trigger.start) + insert + rest,
    caret: trigger.start + insert.length,
  };
}

/** The key's effect on an open list: move the selection, insert, close or nothing. */
export type ListKey = 'down' | 'up' | 'insert' | 'close';

export function keyAction(key: string, shift: boolean, composing: boolean): ListKey | null {
  if (composing) return null;
  switch (key) {
    case 'ArrowDown':
      return 'down';
    case 'ArrowUp':
      return 'up';
    case 'Tab':
      return shift ? null : 'insert';
    case 'Enter':
      return shift ? null : 'insert';
    case 'Escape':
      return 'close';
    default:
      return null;
  }
}

/** The selection after a move, wrapping at the ends. */
export function moveSelection(selected: number, count: number, key: 'down' | 'up'): number {
  if (count === 0) return 0;
  return key === 'down' ? (selected + 1) % count : (selected - 1 + count) % count;
}

/** The popover's left edge: the caret's x less the field's padding, kept inside the composer. */
export function anchorX(caretX: number, boxWidth: number, popWidth: number): number {
  return Math.max(0, Math.min(caretX - 8, boxWidth - popWidth));
}

/** The source tag of a command. */
export function sourceLabel(c: Command): string {
  switch (c.source) {
    case 'builtin':
      return t('Built-in');
    case 'project':
      return t('Project');
    case 'user':
      return t('User');
    case 'plugin':
      return c.plugin ? t('Plugin {name}').replace('{name}', c.plugin) : t('Plugin');
  }
}
