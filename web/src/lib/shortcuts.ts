// The keyboard shortcuts of the web UI (docs/spec.md#command-palette-and-keyboard-shortcuts): one list that the
// Keyboard shortcuts dialog shows, that menus and buttons read their key hints from, and whose page chords the
// cockpit matches. A key that only acts inside one control (the composer, the viewer) is listed for the dialog;
// that control handles it.

export type ShortcutGroup = 'General' | 'Sessions' | 'Workspace' | 'Composer' | 'Palette';

/** A key with its modifiers. ⌘ is Meta on macOS and Ctrl elsewhere; `key` is compared without case. */
export type Chord = {
  mod: boolean;
  alt?: boolean;
  shift?: boolean;
  /** The key as typed (`KeyboardEvent.key`), so a layout's own letters count. */
  key?: string;
  /** The physical key (`KeyboardEvent.code`), for ⌥ chords whose typed key turns into another character on macOS. */
  code?: string;
};

export type Shortcut = {
  id: string;
  group: ShortcutGroup;
  /** The keys as shown, one badge each. */
  keys: readonly string[];
  /** The English label; shown through `t`. */
  label: string;
  /** A browser tab may take this chord first; the dialog marks it with `*`. */
  browser?: true;
  /** The page-level chords that trigger it, in the order of `keys`; none for keys a control handles. */
  chords?: readonly Chord[];
};

const mod = (rest: Omit<Chord, 'mod'>): Chord => ({ mod: true, ...rest });

export const SHORTCUTS = [
  {
    id: 'palette',
    group: 'General',
    keys: ['⌘K'],
    label: 'Search and commands',
    chords: [mod({ key: 'k' })],
  },
  {
    id: 'new-session',
    group: 'General',
    keys: ['⌘N'],
    label: 'New session',
    browser: true,
    chords: [mod({ key: 'n' })],
  },
  {
    id: 'sidebar',
    group: 'General',
    keys: ['⌘\\'],
    label: 'Show or hide the sidebar',
    chords: [mod({ key: '\\' })],
  },
  {
    id: 'settings',
    group: 'General',
    keys: ['⌘,'],
    label: 'Settings',
    chords: [mod({ key: ',' })],
  },
  {
    id: 'shortcuts',
    group: 'General',
    keys: ['⌘/'],
    label: 'Keyboard shortcuts',
    chords: [mod({ key: '/' })],
  },
  {
    id: 'adjacent-session',
    group: 'Sessions',
    keys: ['⌥⌘↑', '⌥⌘↓'],
    label: 'Previous or next session in the sidebar',
    browser: true,
    chords: [mod({ alt: true, key: 'ArrowUp' }), mod({ alt: true, key: 'ArrowDown' })],
  },
  {
    id: 'rename',
    group: 'Sessions',
    keys: ['⌥⌘R'],
    label: 'Rename',
    chords: [mod({ alt: true, code: 'KeyR' })],
  },
  {
    id: 'pin',
    group: 'Sessions',
    keys: ['⌥⌘P'],
    label: 'Pin or unpin',
    chords: [mod({ alt: true, code: 'KeyP' })],
  },
  {
    id: 'tags',
    group: 'Sessions',
    keys: ['⌥⌘T'],
    label: 'Edit tags',
    chords: [mod({ alt: true, code: 'KeyT' })],
  },
  {
    id: 'archive',
    group: 'Sessions',
    keys: ['⇧⌘A'],
    label: 'Archive or restore',
    browser: true,
    chords: [mod({ shift: true, code: 'KeyA' })],
  },
  {
    id: 'workspace',
    group: 'Workspace',
    keys: ['⌘I'],
    label: 'Show or hide the workspace',
    browser: true,
    chords: [mod({ key: 'i' })],
  },
  {
    id: 'changes',
    group: 'Workspace',
    keys: ['⇧⌘G'],
    label: 'Changes',
    chords: [mod({ shift: true, key: 'g' })],
  },
  {
    id: 'files',
    group: 'Workspace',
    keys: ['⇧⌘E'],
    label: 'Files',
    chords: [mod({ shift: true, key: 'e' })],
  },
  { id: 'wrap', group: 'Workspace', keys: ['⌥Z'], label: 'Wrap lines in the viewer' },
  {
    id: 'changed-file',
    group: 'Workspace',
    keys: ['⌥↑', '⌥↓'],
    label: 'Previous or next changed file',
  },
  { id: 'close-viewer', group: 'Workspace', keys: ['esc'], label: 'Close the viewer' },
  { id: 'send', group: 'Composer', keys: ['↩'], label: 'Send' },
  { id: 'new-line', group: 'Composer', keys: ['⇧↩'], label: 'New line' },
  {
    id: 'send-other-way',
    group: 'Composer',
    keys: ['⌥↩'],
    label: 'While it works: send the other way (queue or steer)',
  },
  { id: 'permission-mode', group: 'Composer', keys: ['⇧Tab'], label: 'Next permission mode' },
  { id: 'move-queued', group: 'Composer', keys: ['⌥↑', '⌥↓'], label: 'Move a queued message' },
  {
    id: 'interrupt',
    group: 'Composer',
    keys: ['esc'],
    label: 'While it works: interrupt the turn',
  },
  { id: 'palette-move', group: 'Palette', keys: ['↑', '↓'], label: 'Move' },
  { id: 'palette-open', group: 'Palette', keys: ['↩'], label: 'Open or run' },
  {
    id: 'palette-number',
    group: 'Palette',
    keys: ['⌃1', '…', '⌃9'],
    label: 'Open a listed session',
  },
  {
    id: 'palette-new',
    group: 'Palette',
    keys: ['⌘↩'],
    label: 'New session with the text as the prompt',
  },
  { id: 'palette-back', group: 'Palette', keys: ['esc'], label: 'Back or close' },
] as const satisfies readonly Shortcut[];

export type ShortcutId = (typeof SHORTCUTS)[number]['id'];

/** The groups in the order the dialog lists them. */
export const SHORTCUT_GROUPS: readonly ShortcutGroup[] = [
  'General',
  'Sessions',
  'Workspace',
  'Composer',
  'Palette',
];

const byId = new Map<string, Shortcut>(SHORTCUTS.map((s) => [s.id, s]));

/** The keys of a shortcut as shown: `keys('pin')` is `['⌥⌘P']`. */
export function keysOf(id: ShortcutId): readonly string[] {
  return byId.get(id)?.keys ?? [];
}

/** The first key of a shortcut, for a menu item's or a tooltip's hint. */
export function keyOf(id: ShortcutId): string {
  return keysOf(id)[0] ?? '';
}

/** The fields of a key event a chord is matched on. */
export type KeyLike = Pick<
  KeyboardEvent,
  'key' | 'code' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey'
>;

export function chordMatches(e: KeyLike, chord: Chord): boolean {
  if ((e.metaKey || e.ctrlKey) !== chord.mod) return false;
  if (e.altKey !== (chord.alt ?? false) || e.shiftKey !== (chord.shift ?? false)) return false;
  if (chord.code !== undefined) return e.code === chord.code;
  return e.key.toLowerCase() === (chord.key ?? '').toLowerCase();
}

/** The page shortcut a key event triggers, with which of its chords (0 for ⌥⌘↑, 1 for ⌥⌘↓); null for none. */
export function shortcutOf(e: KeyLike): { id: ShortcutId; at: number } | null {
  for (const s of SHORTCUTS as readonly Shortcut[]) {
    const at = s.chords?.findIndex((c) => chordMatches(e, c)) ?? -1;
    if (at >= 0) return { id: s.id as ShortcutId, at };
  }
  return null;
}

/** The shortcuts of each group whose label or keys hold the filter (the label as the reader sees it). */
export function filterShortcuts(
  filter: string,
  label: (s: Shortcut) => string,
): { group: ShortcutGroup; items: Shortcut[] }[] {
  const f = filter.trim().toLowerCase();
  return SHORTCUT_GROUPS.map((group) => ({
    group,
    items: (SHORTCUTS as readonly Shortcut[]).filter(
      (s) =>
        s.group === group &&
        (!f ||
          label(s).toLowerCase().includes(f) ||
          s.keys.some((k) => k.toLowerCase().includes(f))),
    ),
  })).filter((g) => g.items.length > 0);
}
