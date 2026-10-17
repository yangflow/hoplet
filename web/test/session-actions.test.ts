// @vitest-environment jsdom
import type { DeviceInfo, Entry, LiveSessionMeta } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Menu, type MenuEntry } from '../src/components/Menu.tsx';
import { TOAST_MS, Toast, useToast } from '../src/components/Toast.tsx';
import { ArchivedPage, archivedCount, withSessions } from '../src/screens/cockpit/archived.tsx';
import { buildTree, displayTitle, isArchived, isPinned } from '../src/screens/cockpit/group.ts';
import { lastAnswerRow } from '../src/screens/cockpit/last-answer.ts';
import { RenameDialog } from '../src/screens/cockpit/rename-dialog.tsx';
import { type SessionActions, sessionMenu } from '../src/screens/cockpit/session-actions.tsx';
import { flatList, pinnedList, sortedDirs } from '../src/screens/cockpit/sidebar/sidebar-view.ts';

const clip = vi.hoisted(() => ({ copyText: vi.fn(), copyLater: vi.fn() }));
vi.mock('../src/lib/clipboard.ts', () => clip);

const meta = (id: string, over: Partial<LiveSessionMeta> = {}): LiveSessionMeta =>
  ({
    sessionId: id,
    title: `vendor ${id}`,
    deviceName: 'MacBook Pro',
    ownerDeviceId: 'dev-mac',
    projectPath: '/w/app',
    projectDir: '-w-app',
    provider: 'claude',
    ownerConnected: true,
    createdAt: 1,
    updatedAt: 1,
    lineCount: 1,
    byteSize: 1,
    ownerEpoch: 0,
    ...over,
  }) as LiveSessionMeta;
const mac: DeviceInfo = { deviceId: 'dev-mac', name: 'MacBook Pro', connected: true };

describe('session state in the list', () => {
  it('shows the override, else the vendor title', () => {
    expect(displayTitle({ title: 'vendor', titleOverride: 'Mine' })).toBe('Mine');
    expect(displayTitle({ title: 'vendor' })).toBe('vendor');
    expect(displayTitle({ title: '', titleOverride: '  ' })).toBe('Untitled session');
  });

  it('separates pinned from unpinned sessions and keeps the machine sort in each', () => {
    const list = [
      meta('a', { updatedAt: 10 }),
      meta('b', { updatedAt: 30, pinnedAt: 5 }),
      meta('c', { updatedAt: 20, pinnedAt: 6, projectPath: '/w/other', projectDir: '-w-other' }),
      meta('d', { updatedAt: 40 }),
    ];
    const [group] = buildTree(list, [mac], {});
    if (!group) throw new Error('no group');
    expect(pinnedList(group, 'activity').map((p) => p.session.sessionId)).toEqual(['b', 'c']);
    expect(pinnedList(group, 'activity').map((p) => p.dir)).toEqual(['app', 'other']);
    expect(flatList(group, 'activity').map((p) => p.session.sessionId)).toEqual(['d', 'a']);
    const inFolders = sortedDirs(group, 'activity').flatMap((d) =>
      d.sessions.map((s) => s.sessionId),
    );
    expect(inFolders).not.toContain('b');
    expect(inFolders).not.toContain('c');
    expect(isPinned(list[1] as LiveSessionMeta)).toBe(true);
    expect(isArchived(list[1] as LiveSessionMeta)).toBe(false);
  });
});

describe('lastAnswerRow', () => {
  const text = (lineNo: number, role: 'user' | 'assistant', body: string): Entry => ({
    lineNo,
    role,
    ts: `2026-10-05T09:00:0${lineNo}`,
    blocks: [{ type: 'text', text: body }],
  });
  it('is the newest assistant text, or null when there is none', () => {
    expect(lastAnswerRow([])).toBeNull();
    expect(lastAnswerRow([text(0, 'user', 'hi')])).toBeNull();
    const row = lastAnswerRow([
      text(0, 'user', 'hi'),
      text(1, 'assistant', 'first'),
      text(2, 'user', 'more'),
      text(3, 'assistant', 'second'),
    ]);
    expect(row?.text).toBe('second');
  });
});

let root: Root;
let host: HTMLDivElement;
const q = (id: string) => host.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const qa = (id: string) => [...host.querySelectorAll<HTMLElement>(`[data-testid="${id}"]`)];
const render = (node: Parameters<Root['render']>[0]) => act(async () => root.render(node));
const key = (el: Element, k: string) =>
  act(async () => {
    el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }));
  });

beforeEach(() => {
  vi.resetAllMocks();
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  Object.defineProperty(navigator, 'language', { configurable: true, value: 'en-US' });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  vi.useRealTimers();
});

describe('sessionMenu', () => {
  const actions = () =>
    ({
      patch: vi.fn(async () => {}),
      rename: vi.fn(),
      remove: vi.fn(),
      say: vi.fn(),
    }) satisfies SessionActions;
  const find = (entries: MenuEntry[], id: string) => {
    for (const e of entries) {
      if (e.id === id) return e;
      if (e.kind === 'submenu') {
        const inner = e.items.find((i) => i.id === id);
        if (inner) return inner;
      }
    }
    throw new Error(id);
  };
  const run = async (entries: MenuEntry[], id: string) => {
    const e = find(entries, id);
    if (e.kind !== 'item') throw new Error('not an item');
    await act(async () => e.onSelect());
  };

  it('lists the groups of the design in order', () => {
    const ids = sessionMenu(meta('a'), actions()).map((e) => e.id);
    expect(ids).toEqual([
      'rename',
      'pin',
      'sep-1',
      'copy',
      'open',
      'sep-2',
      'archive',
      'sep-3',
      'delete',
    ]);
  });

  it('ends with a danger Delete entry that asks to remove the session', async () => {
    const a = actions();
    const s = meta('a');
    const entries = sessionMenu(s, a);
    const del = find(entries, 'delete');
    expect(del.kind === 'item' && del.danger).toBe(true);
    await run(entries, 'delete');
    expect(a.remove).toHaveBeenCalledWith(s);
  });

  it('pins and archives, or undoes it, by the state of the session', async () => {
    const a = actions();
    await run(sessionMenu(meta('a'), a), 'pin');
    await run(sessionMenu(meta('a'), a), 'archive');
    await run(sessionMenu(meta('a', { pinnedAt: 1, archivedAt: 2 }), a), 'pin');
    await run(sessionMenu(meta('a', { pinnedAt: 1, archivedAt: 2 }), a), 'archive');
    expect(a.patch.mock.calls).toEqual([
      ['a', { pinned: true }],
      ['a', { archived: true }],
      ['a', { pinned: false }],
      ['a', { archived: false }],
    ]);
    const labels = sessionMenu(meta('a', { pinnedAt: 1, archivedAt: 2 }), a).map((e) =>
      e.kind === 'separator' ? '' : e.label,
    );
    expect(labels).toContain('Unpin');
    expect(labels).toContain('Unarchive');
  });

  it('renames through the dialog and copies the id and the link', async () => {
    const a = actions();
    clip.copyText.mockResolvedValue(true);
    const s = meta('abc');
    const entries = sessionMenu(s, a);
    await run(entries, 'rename');
    expect(a.rename).toHaveBeenCalledWith(s);
    await run(entries, 'copy-id');
    expect(clip.copyText).toHaveBeenLastCalledWith('abc');
    await run(entries, 'copy-link');
    expect(clip.copyText).toHaveBeenLastCalledWith(`${location.origin}/?session=abc`);
    expect(a.say.mock.calls).toEqual([['Session ID copied'], ['Link copied']]);
  });

  it('says so when the copy fails', async () => {
    const a = actions();
    clip.copyText.mockResolvedValue(false);
    await run(sessionMenu(meta('abc'), a), 'copy-id');
    expect(a.say).toHaveBeenCalledWith('Could not copy', true);
  });

  it('copies the last answer through a write that starts inside the click', async () => {
    const a = actions();
    clip.copyLater.mockImplementation(async (get: () => Promise<string | null>) => {
      await get().catch(() => null);
      return 'copied';
    });
    await run(sessionMenu(meta('abc'), a), 'copy-answer');
    expect(clip.copyLater).toHaveBeenCalledOnce();
    expect(a.say).toHaveBeenLastCalledWith('Answer copied');
    clip.copyLater.mockResolvedValue('empty');
    await run(sessionMenu(meta('abc'), a), 'copy-answer');
    expect(a.say).toHaveBeenLastCalledWith('No answer to copy', true);
    clip.copyLater.mockResolvedValue('failed');
    await run(sessionMenu(meta('abc'), a), 'copy-answer');
    expect(a.say).toHaveBeenLastCalledWith('Could not copy', true);
  });

  it('opens the session in a new window', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    await run(sessionMenu(meta('abc'), actions()), 'open');
    expect(open).toHaveBeenCalledWith(
      `${location.origin}/?session=abc`,
      '_blank',
      expect.stringContaining('noopener'),
    );
  });
});

describe('Menu', () => {
  const entries = (onSelect: () => void, onSub: () => void): MenuEntry[] => [
    { kind: 'item', id: 'one', label: 'One', onSelect },
    { kind: 'separator', id: 'sep' },
    {
      kind: 'submenu',
      id: 'more',
      label: 'More',
      items: [{ kind: 'item', id: 'inner', label: 'Inner', onSelect: onSub }],
    },
    { kind: 'item', id: 'last', label: 'Last', onSelect: () => {} },
  ];

  it('moves with the arrows, opens a submenu to the right and closes it to the left', async () => {
    const onClose = vi.fn();
    const onSub = vi.fn();
    await render(
      createElement(Menu, { label: 'Actions', entries: entries(() => {}, onSub), onClose }),
    );
    const items = () => qa('menu.one').concat(qa('menu.more'), qa('menu.last'));
    expect(document.activeElement).toBe(q('menu.one'));
    await key(document.activeElement as Element, 'ArrowDown');
    expect(document.activeElement).toBe(q('menu.more'));
    await key(document.activeElement as Element, 'ArrowRight');
    expect(q('menu.inner')).not.toBeNull();
    expect(document.activeElement).toBe(q('menu.inner'));
    await key(document.activeElement as Element, 'ArrowLeft');
    expect(q('menu.inner')).toBeNull();
    expect(document.activeElement).toBe(q('menu.more'));
    await key(document.activeElement as Element, 'End');
    expect(document.activeElement).toBe(q('menu.last'));
    await key(document.activeElement as Element, 'Home');
    expect(document.activeElement).toBe(items()[0]);
  });

  it('closes with the focus restored on Esc, and runs the choice after closing', async () => {
    const onClose = vi.fn();
    const onSelect = vi.fn();
    await render(
      createElement(Menu, { label: 'Actions', entries: entries(onSelect, () => {}), onClose }),
    );
    await key(q('menu.one') as Element, 'Escape');
    expect(onClose).toHaveBeenLastCalledWith(true);
    await act(async () => q('menu.one')?.click());
    // A chosen command gives the focus back to the trigger before it runs, so a dialog it opens returns there.
    expect(onClose).toHaveBeenLastCalledWith(true);
    expect(onSelect).toHaveBeenCalledOnce();
  });

  it('shows the focus ring only for a keyboard-opened menu or after a key', async () => {
    await render(
      createElement(Menu, {
        label: 'Actions',
        entries: entries(
          () => {},
          () => {},
        ),
        onClose: () => {},
      }),
    );
    expect(q('menu.one')?.closest('[role="menu"]')?.hasAttribute('data-kb')).toBe(false);
    await key(q('menu.one') as Element, 'ArrowDown');
    expect(q('menu.one')?.closest('[role="menu"]')?.getAttribute('data-kb')).toBe('true');
    await render(
      createElement(Menu, {
        label: 'Actions',
        entries: entries(
          () => {},
          () => {},
        ),
        onClose: () => {},
        keyboard: true,
      }),
    );
    expect(q('menu.one')?.closest('[role="menu"]')?.getAttribute('data-kb')).toBe('true');
  });

  it('opens with a submenu already out', async () => {
    await render(
      createElement(Menu, {
        label: 'Actions',
        entries: entries(
          () => {},
          () => {},
        ),
        onClose: () => {},
        initialSub: 'more',
      }),
    );
    expect(q('menu.inner')).not.toBeNull();
  });
});

describe('Toast', () => {
  it('shows a message and clears itself', async () => {
    vi.useFakeTimers();
    let show: (text: string, failed?: boolean) => void = () => {};
    function Probe() {
      const [toast, say] = useToast();
      show = say;
      return toast ? createElement(Toast, { failed: toast.failed }, toast.text) : null;
    }
    await render(createElement(Probe));
    expect(q('toast')).toBeNull();
    await act(async () => show('Copied'));
    expect(q('toast')?.textContent).toBe('Copied');
    expect(q('toast')?.getAttribute('role')).toBe('status');
    await act(async () => show('Could not copy', true));
    expect(q('toast')?.className).toContain('failed');
    await act(async () => vi.advanceTimersByTime(TOAST_MS + 1));
    expect(q('toast')).toBeNull();
  });
});

describe('RenameDialog', () => {
  const mount = (session: LiveSessionMeta, onSave = vi.fn(), onClose = vi.fn()) =>
    render(createElement(RenameDialog, { session, onSave, onClose }));
  const type = (value: string) =>
    act(async () => {
      const field = q('cockpit.rename-field') as HTMLInputElement;
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
      set?.call(field, value);
      field.dispatchEvent(new Event('input', { bubbles: true }));
    });
  const save = () => act(async () => q('cockpit.rename-save')?.click());

  it('starts from the shown title and names the vendor title beside it', async () => {
    await mount(meta('a', { titleOverride: 'Mine' }));
    expect((q('cockpit.rename-field') as HTMLInputElement).value).toBe('Mine');
    expect(q('cockpit.rename-vendor')?.textContent).toBe('vendor a');
  });

  it('saves the new title trimmed', async () => {
    const onSave = vi.fn();
    await mount(meta('a'), onSave);
    await type('  New name ');
    await save();
    expect(onSave).toHaveBeenCalledWith('New name');
  });

  it('saves none when the field is cleared or holds the vendor title', async () => {
    const onSave = vi.fn();
    await mount(meta('a', { titleOverride: 'Mine' }), onSave);
    await type('   ');
    await save();
    await type('vendor a');
    await save();
    expect(onSave.mock.calls).toEqual([[null], [null]]);
  });

  it('fills in the vendor title on "Use Claude’s title"', async () => {
    await mount(meta('a', { titleOverride: 'Mine' }));
    await act(async () => q('cockpit.rename-reset')?.click());
    expect((q('cockpit.rename-field') as HTMLInputElement).value).toBe('vendor a');
  });
});

describe('ArchivedPage', () => {
  const groups = () =>
    withSessions(
      buildTree(
        [
          meta('a', { archivedAt: 1, updatedAt: 5 }),
          meta('b', { archivedAt: 1, updatedAt: 9 }),
          meta('c', {
            archivedAt: 1,
            deviceName: 'office',
            ownerDeviceId: 'dev-off',
            ownerConnected: false,
          }),
        ],
        [mac, { deviceId: 'dev-off', name: 'office', connected: false }],
        {},
      ),
    );

  it('groups by machine and folder and counts', async () => {
    const g = groups();
    expect(archivedCount(g)).toBe(3);
    await render(
      createElement(ArchivedPage, { groups: g, now: 100, onRestore: vi.fn(), onBack: vi.fn() }),
    );
    expect(qa('cockpit.archived-machine')).toHaveLength(2);
    expect(qa('cockpit.archived-folder')).toHaveLength(2);
    expect(qa('cockpit.archived-session')).toHaveLength(3);
    expect(q('cockpit.archived-page')?.textContent).toContain('3');
    expect(host.textContent).toContain('Offline');
  });

  it('restores one session or a whole folder without asking', async () => {
    const onRestore = vi.fn();
    await render(
      createElement(ArchivedPage, { groups: groups(), now: 100, onRestore, onBack: vi.fn() }),
    );
    await act(async () => qa('cockpit.restore')[0]?.click());
    expect(onRestore).toHaveBeenLastCalledWith(['b']);
    await act(async () => qa('cockpit.restore-all')[0]?.click());
    expect(onRestore).toHaveBeenLastCalledWith(['b', 'a']);
  });

  it('shows an empty state and goes back on the button and on Esc', async () => {
    const onBack = vi.fn();
    await render(createElement(ArchivedPage, { groups: [], now: 1, onRestore: vi.fn(), onBack }));
    expect(q('cockpit.archived-empty')).not.toBeNull();
    await act(async () => q('cockpit.archived-back')?.click());
    await key(document.body, 'Escape');
    expect(onBack).toHaveBeenCalledTimes(2);
  });
});
