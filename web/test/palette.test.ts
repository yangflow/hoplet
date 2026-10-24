// @vitest-environment jsdom
import type { LiveSessionMeta } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Kid } from '../src/lib/children.ts';
import {
  filterShortcuts,
  keyOf,
  keysOf,
  SHORTCUT_GROUPS,
  SHORTCUTS,
  shortcutOf,
} from '../src/lib/shortcuts.ts';
import { buildTree } from '../src/screens/cockpit/group.ts';
import { KeysDialog } from '../src/screens/cockpit/keys-dialog.tsx';
import {
  arrange,
  fuzzy,
  type PaletteItem,
  type PaletteSource,
} from '../src/screens/cockpit/palette-model.ts';
import { CockpitPalette, type PaletteCommands } from '../src/screens/cockpit/palette-sources.tsx';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ev = (over: Partial<KeyboardEventInit> & { key: string }) => ({
  code: '',
  metaKey: false,
  ctrlKey: false,
  altKey: false,
  shiftKey: false,
  ...over,
});

describe('the shortcut registry', () => {
  it('matches each page chord exactly, with ⌘ read as Ctrl too', () => {
    expect(shortcutOf(ev({ key: 'k', metaKey: true }))).toEqual({ id: 'palette', at: 0 });
    expect(shortcutOf(ev({ key: 'K', ctrlKey: true }))).toEqual({ id: 'palette', at: 0 });
    expect(shortcutOf(ev({ key: 'k' }))).toBeNull();
    expect(shortcutOf(ev({ key: 'k', metaKey: true, shiftKey: true }))).toBeNull();
    expect(shortcutOf(ev({ key: '/', metaKey: true }))).toEqual({ id: 'shortcuts', at: 0 });
    expect(shortcutOf(ev({ key: 'ArrowDown', metaKey: true, altKey: true }))).toEqual({
      id: 'adjacent-session',
      at: 1,
    });
  });

  it('matches ⌥ chords on the physical key, whatever character ⌥ types', () => {
    expect(shortcutOf(ev({ key: 'π', code: 'KeyP', metaKey: true, altKey: true }))?.id).toBe('pin');
    expect(shortcutOf(ev({ key: '®', code: 'KeyR', metaKey: true, altKey: true }))?.id).toBe(
      'rename',
    );
  });

  it('gives every shortcut a group the dialog lists, and unique ids', () => {
    const ids = SHORTCUTS.map((s) => s.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const s of SHORTCUTS) expect(SHORTCUT_GROUPS).toContain(s.group);
    expect(keyOf('palette')).toBe('⌘K');
    expect(keysOf('adjacent-session')).toEqual(['⌥⌘↑', '⌥⌘↓']);
  });

  it('filters by label or key and drops the groups left empty', () => {
    const label = (s: { label: string }) => s.label;
    const byKey = filterShortcuts('⌘/', label);
    expect(byKey.map((g) => g.group)).toEqual(['General']);
    expect(byKey[0]?.items.map((s) => s.id)).toEqual(['shortcuts']);
    expect(filterShortcuts('workspace', label).flatMap((g) => g.items.map((s) => s.id))).toContain(
      'workspace',
    );
    expect(filterShortcuts('zzz', label)).toEqual([]);
  });
});

describe('fuzzy and arrange', () => {
  it('ranks a run at the start above one at a word, above scattered letters', () => {
    const start = fuzzy('Stop', 'st');
    const word = fuzzy('Keyboard shortcuts', 'sh');
    const scattered = fuzzy('Settings', 'sg');
    expect(start?.at).toEqual([0, 1]);
    expect(scattered?.at).toEqual([0, 6]);
    expect((start?.score ?? 0) > (word?.score ?? 0)).toBe(true);
    expect((word?.score ?? 0) > (scattered?.score ?? 0)).toBe(true);
    expect(fuzzy('Stop', 'x')).toBeNull();
  });

  const item = (id: string, label: string, more: Partial<PaletteItem> = {}): PaletteItem => ({
    id,
    kind: 'command',
    label,
    lead: null,
    run: () => {},
    ...more,
  });

  it('keeps the sources in order, merges sections that share a label and numbers sessions', () => {
    const sources: PaletteSource[] = [
      {
        id: 'recent',
        label: 'Recent',
        when: 'empty',
        limit: 1,
        items: [item('s1', 'one', { numbered: true }), item('s2', 'two', { numbered: true })],
      },
      {
        id: 'this',
        label: 'This session',
        queryLabel: 'Commands',
        items: [item('c1', 'Stop'), item('c2', 'Rename', { queryOnly: true })],
      },
      { id: 'general', label: 'General', queryLabel: 'Commands', items: [item('c3', 'Settings')] },
    ];
    const empty = arrange(sources, '');
    expect(empty.map((s) => [s.label, s.items.map((i) => i.item.id)])).toEqual([
      ['Recent', ['s1']],
      ['This session', ['c1']],
      ['General', ['c3']],
    ]);
    expect(empty[0]?.items[0]?.key).toBe('^1');
    const typed = arrange(sources, 'st');
    expect(typed.map((s) => [s.label, s.items.map((i) => i.item.id)])).toEqual([
      ['Commands', ['c1', 'c3']],
    ]);
  });
});

const session = (id: string, title: string, over: Partial<LiveSessionMeta> = {}) =>
  ({
    sessionId: id,
    projectPath: '/w/hoplet',
    projectDir: '-w-hoplet',
    deviceName: 'MacBook Pro',
    ownerDeviceId: 'dev-mac',
    title,
    provider: 'claude',
    ownerConnected: true,
    createdAt: 1,
    updatedAt: 2,
    lineCount: 1,
    byteSize: 1,
    ownerEpoch: 0,
    ...over,
  }) as LiveSessionMeta;

const CATALOG = session('s1', 'byte cursor catalog', { updatedAt: 3 });
const NOTES = session('s2', 'release notes');
const KID: Kid = {
  id: 'k1',
  parentId: 's1',
  depth: 1,
  title: 'docs pass',
  machine: 'build-box',
  branch: null,
  state: 'running',
  elapsedSeconds: 12,
  childCount: 0,
};

const commandsOf = (over: Partial<PaletteCommands> = {}): PaletteCommands => ({
  session: CATALOG,
  running: true,
  stopsWith: [KID],
  workspaceOpen: false,
  sidebarShown: true,
  stop: vi.fn(),
  handoff: vi.fn(),
  rename: vi.fn(),
  tags: vi.fn(),
  patch: vi.fn(),
  remove: vi.fn(),
  workspace: vi.fn(),
  tool: vi.fn(),
  adjacent: vi.fn(),
  newSession: vi.fn(),
  sidebar: vi.fn(),
  shortcuts: vi.fn(),
  settings: vi.fn(),
  ...over,
});

let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  document.body.innerHTML = '';
});

const key = async (el: Element, k: string, init: KeyboardEventInit = {}) => {
  await act(async () => {
    el.dispatchEvent(
      new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...init }),
    );
  });
};
const type = async (input: HTMLInputElement, text: string) => {
  await act(async () => {
    const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
    set?.call(input, text);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
};
const byTest = (id: string) => [...document.querySelectorAll(`[data-testid="${id}"]`)];
const input = () => document.querySelector('input[role="combobox"]') as HTMLInputElement;

function palette(over: Partial<Parameters<typeof CockpitPalette>[0]> = {}) {
  const props = {
    sessions: [CATALOG, NOTES],
    groups: buildTree([CATALOG, NOTES], []),
    searchFixture: { hits: [], scanning: false },
    commands: commandsOf(),
    onClose: vi.fn(),
    onSelectSession: vi.fn(),
    onSelectHit: vi.fn(),
    onMachine: vi.fn(),
    onStartIn: vi.fn(),
    onNewWithText: vi.fn(),
    ...over,
  };
  return { props, element: createElement(CockpitPalette, props) };
}

describe('the command palette', () => {
  it('lists recent sessions and commands, and opens the active row on Enter', async () => {
    const { props, element } = palette();
    await act(async () => root.render(element));
    expect(document.activeElement).toBe(input());
    expect(byTest('cockpit.palette-session').map((o) => o.textContent)).toEqual([
      expect.stringContaining('byte cursor catalog'),
      expect.stringContaining('release notes'),
    ]);
    // Machines and projects are reached by typing.
    expect(byTest('cockpit.palette-machine')).toHaveLength(0);
    expect(byTest('cockpit.palette-project')).toHaveLength(0);
    const commands = byTest('cockpit.palette-command').map((o) => o.textContent ?? '');
    expect(commands.some((c) => c.includes('Stop'))).toBe(true);
    expect(commands.some((c) => c.includes('Keyboard shortcuts'))).toBe(true);
    // Rows only a query reaches are not listed before one is typed.
    expect(commands.some((c) => c.includes('Delete'))).toBe(false);
    await key(input(), 'Enter');
    expect(props.onSelectSession).toHaveBeenCalledWith('s1');
    expect(props.onClose).toHaveBeenCalled();
  });

  it('opens a listed session with ⌃ and its number', async () => {
    const { props, element } = palette();
    await act(async () => root.render(element));
    await key(input(), '2', { ctrlKey: true });
    expect(props.onSelectSession).toHaveBeenCalledWith('s2');
  });

  it('finds commands by fuzzy letters and runs one', async () => {
    const commands = commandsOf();
    const { element } = palette({ commands });
    await act(async () => root.render(element));
    await type(input(), 'kbd');
    const rows = byTest('cockpit.palette-command');
    expect(rows[0]?.textContent).toContain('Keyboard shortcuts');
    await key(input(), 'Enter');
    expect(commands.shortcuts).toHaveBeenCalled();
  });

  it('asks before Stop, names the child that stops with it, and stops on Enter', async () => {
    const commands = commandsOf();
    const { props, element } = palette({ commands, initialQuery: 'stop' });
    await act(async () => root.render(element));
    await key(input(), 'Enter');
    expect(commands.stop).not.toHaveBeenCalled();
    const confirm = byTest('cockpit.palette-confirm')[0];
    expect(confirm?.textContent).toContain('Stop “byte cursor catalog”?');
    expect(confirm?.textContent).toContain('Its child session stops with it:');
    expect(byTest('cockpit.palette-confirm-list')[0]?.textContent).toContain('docs pass');
    // Esc goes back to the list and keeps the palette open.
    await key(input(), 'Escape');
    expect(byTest('cockpit.palette-confirm')).toHaveLength(0);
    expect(props.onClose).not.toHaveBeenCalled();
    await key(input(), 'Enter');
    await key(input(), 'Enter');
    expect(commands.stop).toHaveBeenCalledWith(CATALOG, true);
    expect(props.onClose).toHaveBeenCalled();
  });

  it('stops a session without children without cascading', async () => {
    const commands = commandsOf({ stopsWith: [] });
    const { element } = palette({ commands, initialQuery: 'stop', confirming: 'c-stop' });
    await act(async () => root.render(element));
    expect(byTest('cockpit.palette-confirm-list')).toHaveLength(0);
    await key(input(), 'Enter');
    expect(commands.stop).toHaveBeenCalledWith(CATALOG, false);
  });

  it('offers no Stop while the session is idle', async () => {
    const { element } = palette({ commands: commandsOf({ running: false }) });
    await act(async () => root.render(element));
    const rows = byTest('cockpit.palette-command').map((o) => o.textContent ?? '');
    expect(rows.some((c) => c.startsWith('Stop'))).toBe(false);
  });

  it('offers a new session with the text when nothing matches, and ⌘↩ starts one from any query', async () => {
    const { props, element } = palette({ initialQuery: 'qqzzx' });
    await act(async () => root.render(element));
    expect(byTest('cockpit.palette-none')).toHaveLength(1);
    expect(byTest('cockpit.palette-new-with-text')[0]?.textContent).toContain('“qqzzx”');
    await key(input(), 'Enter');
    expect(props.onNewWithText).toHaveBeenCalledWith('qqzzx');

    await act(async () => root.unmount());
    root = createRoot(host);
    const next = palette({ initialQuery: 'catalog' });
    await act(async () => root.render(next.element));
    await key(input(), 'Enter', { metaKey: true });
    expect(next.props.onNewWithText).toHaveBeenCalledWith('catalog');
    expect(next.props.onSelectSession).not.toHaveBeenCalled();
  });

  it('finds a machine, and starts a session in a project on the machine of the open session', async () => {
    const machine = palette({ initialQuery: 'macbook' });
    await act(async () => root.render(machine.element));
    await act(async () => (byTest('cockpit.palette-machine')[0] as HTMLElement).click());
    expect(machine.props.onMachine).toHaveBeenCalledWith('dev-mac');
    await act(async () => root.unmount());
    root = createRoot(host);
    const { props, element } = palette({ initialQuery: 'hoplet' });
    await act(async () => root.render(element));
    const project = byTest('cockpit.palette-project')[0] as HTMLElement;
    await act(async () => project.click());
    expect(props.onStartIn).toHaveBeenCalledWith({ deviceName: 'MacBook Pro', path: '/w/hoplet' });
  });
});

describe('the Keyboard shortcuts dialog', () => {
  it('lists every group, marks the chords a browser may take, and filters', async () => {
    await act(async () => root.render(createElement(KeysDialog, { onClose: vi.fn() })));
    expect(
      byTest('cockpit.shortcuts-group').map((g) => g.querySelector('h3')?.textContent),
    ).toEqual(['General', 'Sessions', 'Palette', 'Workspace', 'Composer']);
    const rows = byTest('cockpit.shortcut');
    expect(rows).toHaveLength(SHORTCUTS.length);
    const newSession = rows.find((r) => r.textContent?.startsWith('New session'));
    expect(newSession?.querySelector('.fn')?.textContent).toBe('*');
    const filter = byTest('cockpit.shortcuts-filter')[0] as HTMLInputElement;
    expect(document.activeElement).toBe(filter);
    await type(filter, 'sidebar');
    expect(byTest('cockpit.shortcut').map((r) => r.querySelector('.d')?.textContent)).toEqual([
      'Show or hide the sidebar',
      'Previous or next session in the sidebar*',
    ]);
    await type(filter, 'zzz');
    expect(document.body.textContent).toContain('No shortcut matches “zzz”');
  });
});
