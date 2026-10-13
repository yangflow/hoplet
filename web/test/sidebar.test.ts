// @vitest-environment jsdom
import type { LiveSessionMeta } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { buildTree } from '../src/screens/cockpit/group.ts';
import type { RuntimeState } from '../src/screens/cockpit/model.ts';
import { FOLDED_KEY } from '../src/screens/cockpit/sidebar/folded-dirs.ts';
import { Sidebar } from '../src/screens/cockpit/sidebar/sidebar.tsx';
import { VIEWS_KEY } from '../src/screens/cockpit/sidebar/sidebar-view.ts';

let root: Root;
let host: HTMLDivElement;

const meta = (
  id: string,
  title: string,
  dir: string,
  over: Partial<LiveSessionMeta> = {},
): LiveSessionMeta =>
  ({
    sessionId: id,
    title,
    deviceName: 'studio',
    projectPath: `/w/${dir}`,
    projectDir: `-w-${dir}`,
    provider: 'claude',
    ownerConnected: true,
    updatedAt: 0,
    createdAt: 0,
    ...over,
  }) as LiveSessionMeta;

const sessions = [meta('a', 'one', 'notes'), meta('b', 'two', 'notes'), meta('c', 'three', 'app')];

async function show(
  runtime: Record<string, RuntimeState> = {},
  foldedDirs?: string[],
  selected: string | null = null,
  list: LiveSessionMeta[] = sessions,
) {
  await act(async () =>
    root.render(
      createElement(Sidebar, {
        groups: buildTree(list, []),
        selected,
        runtime,
        now: 0,
        host: 'hoplet.local',
        live: true,
        newSessionOpen: false,
        ...(foldedDirs ? { foldedDirs } : {}),
        onSelect: () => undefined,
        onNewSession: () => undefined,
        onAddProject: () => undefined,
        onSearch: () => undefined,
        onFold: () => undefined,
        onSettings: () => undefined,
      }),
    ),
  );
}
const toggles = () => [
  ...host.querySelectorAll<HTMLButtonElement>('[data-testid="cockpit.directory-toggle"]'),
];
const titles = () =>
  [...host.querySelectorAll('[data-testid="cockpit.session"]')].map(
    (r) => (r.querySelector('.two-title') ?? r.querySelector('.row-label'))?.textContent,
  );

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  Object.defineProperty(navigator, 'language', { configurable: true, value: 'en-US' });
  localStorage.clear();
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  localStorage.clear();
});

describe('sidebar folding', () => {
  it('lists the directories open, each a button with aria-expanded', async () => {
    await show();
    expect(
      toggles().map((b) => [b.querySelector('.dn')?.textContent, b.getAttribute('aria-expanded')]),
    ).toEqual([
      ['app', 'true'],
      ['notes', 'true'],
    ]);
    expect(titles()).toHaveLength(3);
  });

  it('folds a directory on press, remembers it, and shows its count and most urgent state', async () => {
    await show({ a: 'running', b: 'waiting-permission' });
    const notes = toggles()[1] as HTMLButtonElement;
    await act(async () => notes.click());
    expect(toggles()[1]?.getAttribute('aria-expanded')).toBe('false');
    expect(titles()).toEqual(['three']);
    const row = toggles()[1] as HTMLElement;
    expect(row.querySelector('[data-testid="cockpit.directory-count"]')?.textContent).toBe('2');
    expect(row.querySelector('.glyph')?.getAttribute('aria-label')).toBe('Waiting for permission');
    expect(JSON.parse(localStorage.getItem(FOLDED_KEY) ?? '[]')).toHaveLength(1);
    await act(async () => (toggles()[1] as HTMLButtonElement).click());
    expect(titles()).toHaveLength(3);
    expect(JSON.parse(localStorage.getItem(FOLDED_KEY) ?? '[]')).toHaveLength(0);
  });

  it('starts from the folded directories the browser remembers', async () => {
    localStorage.setItem(FOLDED_KEY, JSON.stringify([`studio\u0000/w/app`]));
    await show();
    expect(toggles().map((b) => b.getAttribute('aria-expanded'))).toEqual(['false', 'true']);
    expect(titles()).toEqual(['one', 'two']);
  });

  it('shows no state glyph for a folded directory whose sessions are only running', async () => {
    await show({ a: 'running' }, [`studio\u0000/w/notes`]);
    const row = toggles()[1] as HTMLElement;
    expect(row.querySelector('.glyph')).toBeNull();
    expect(row.querySelector('[data-testid="cockpit.directory-count"]')?.textContent).toBe('2');
  });
});

describe('machine rows', () => {
  const toggle = () =>
    host.querySelector<HTMLButtonElement>(
      '[data-testid="cockpit.device-toggle"]',
    ) as HTMLButtonElement;
  const add = () =>
    host.querySelector<HTMLButtonElement>(
      '[data-testid="cockpit.add-project"]',
    ) as HTMLButtonElement;

  it('folds all its directories, remembers it per device and unfolds again', async () => {
    await show();
    expect(toggle().getAttribute('aria-expanded')).toBe('true');
    expect(titles()).toHaveLength(3);
    await act(async () => toggle().click());
    expect(toggle().getAttribute('aria-expanded')).toBe('false');
    expect(toggles()).toHaveLength(0);
    expect(titles()).toHaveLength(0);
    expect(JSON.parse(localStorage.getItem(FOLDED_KEY) ?? '[]')).toEqual([`studio\u0000`]);
    await act(async () => toggle().click());
    expect(toggles()).toHaveLength(2);
  });

  it('names the presence in the toggle and offers Add project', async () => {
    await show();
    expect(toggle().getAttribute('aria-label')).toBe('studio · Online');
    expect(add().getAttribute('aria-label')).toBe('Add project');
    expect(add().title).toBe('Add project');
    expect(add().disabled).toBe(false);
  });

  it('shows folder glyphs: closed when folded, open when not', async () => {
    await show({}, [`studio\u0000/w/app`]);
    const glyph = (i: number) => toggles()[i]?.querySelector('.fi svg')?.getAttribute('class');
    expect(glyph(0)).toContain('lucide-folder');
    expect(glyph(0)).not.toContain('lucide-folder-open');
    expect(glyph(1)).toContain('lucide-folder-open');
  });
});

describe('the sidebar follows the selected session', () => {
  const open = (id: string) => show({}, [`studio\u0000/w/notes`, `studio\u0000`], id);

  it('opens the directory that holds it, and leaves other folds alone', async () => {
    localStorage.setItem(
      FOLDED_KEY,
      JSON.stringify([`studio\u0000/w/app`, `studio\u0000/w/notes`]),
    );
    await show({}, undefined, 'a');
    expect(
      toggles().map((b) => [b.querySelector('.dn')?.textContent, b.getAttribute('aria-expanded')]),
    ).toEqual([
      ['app', 'false'],
      ['notes', 'true'],
    ]);
    expect(titles()).toEqual(['one', 'two']);
    expect(JSON.parse(localStorage.getItem(FOLDED_KEY) ?? '[]')).toEqual([`studio\u0000/w/app`]);
  });

  it('opens a folded machine too', async () => {
    await open('a');
    expect(
      host.querySelector('[data-testid="cockpit.device-toggle"]')?.getAttribute('aria-expanded'),
    ).toBe('true');
    expect(titles()).toEqual(['three', 'one', 'two']);
  });

  it('reveals again when the selection changes, but not when the reader folds it afterwards', async () => {
    await show({}, [], 'a');
    await act(async () => (toggles()[1] as HTMLButtonElement).click());
    expect(toggles()[1]?.getAttribute('aria-expanded')).toBe('false');
    await show({}, [], 'a');
    expect(toggles()[1]?.getAttribute('aria-expanded')).toBe('false');
    await show({}, [], 'b');
    expect(toggles()[1]?.getAttribute('aria-expanded')).toBe('true');
    expect(JSON.parse(localStorage.getItem(FOLDED_KEY) ?? '[]')).toEqual([]);
  });

  it('opens it when the session arrives after the selection', async () => {
    localStorage.setItem(FOLDED_KEY, JSON.stringify([`studio\u0000/w/notes`]));
    await show({}, undefined, 'a', []);
    await show({}, undefined, 'a');
    expect(titles()).toEqual(['three', 'one', 'two']);
  });

  it('scrolls the selected row into view', async () => {
    const calls: string[] = [];
    Element.prototype.scrollIntoView = function (this: Element) {
      calls.push(this.getAttribute('aria-current') ?? '');
    };
    await open('a');
    expect(calls).toEqual(['true']);
  });
});

describe('machine view options', () => {
  const menu = (id: string, at = 0) =>
    host.querySelectorAll<HTMLButtonElement>(`[data-testid="${id}"]`)[at] as HTMLButtonElement;
  const pick = async (id: string, machine = 0) => {
    await act(async () => menu('cockpit.machine-menu', machine).click());
    await act(async () => menu(id).click());
  };
  const two = [
    meta('a', 'one', 'notes', { deviceName: 'studio', updatedAt: 30, createdAt: 3 }),
    meta('b', 'two', 'hoplet', { deviceName: 'studio', updatedAt: 10, createdAt: 2 }),
    meta('c', 'three', 'hoplet', { deviceName: 'mac', updatedAt: 20, createdAt: 1 }),
  ];

  it('has no menu in the brand row, one on each machine row', async () => {
    await show({}, undefined, null, two);
    expect(host.querySelector('.brand [data-testid="cockpit.machine-menu"]')).toBeNull();
    expect(host.querySelectorAll('.dev [data-testid="cockpit.machine-menu"]')).toHaveLength(2);
    expect(menu('cockpit.machine-menu').title).toBe('View options');
  });

  it('opens a menu titled with the machine, two radio groups, defaults marked', async () => {
    await show({}, undefined, null, two);
    await act(async () => menu('cockpit.machine-menu', 1).click());
    expect(host.querySelector('.menu-title')?.textContent).toBe('studio');
    const checked = [...host.querySelectorAll('[role="menuitemradio"][aria-checked="true"]')];
    expect(checked.map((c) => c.textContent)).toEqual(['By folder', 'Recent activity']);
    expect(host.querySelectorAll('[role="menuitemradio"]')).toHaveLength(5);
  });

  it('flattens one machine only, with the directory on the second line, and remembers it', async () => {
    await show({}, undefined, null, two);
    await pick('cockpit.organize-flat', 0);
    const mac = host.querySelectorAll('[data-testid="cockpit.device"]')[0] as HTMLElement;
    const studio = host.querySelectorAll('[data-testid="cockpit.device"]')[1] as HTMLElement;
    expect(mac.querySelector('[data-testid="cockpit.directory-toggle"]')).toBeNull();
    expect(
      [...mac.querySelectorAll('[data-testid="cockpit.session-where"]')].map((w) => w.textContent),
    ).toEqual(['hoplet']);
    expect(studio.querySelectorAll('[data-testid="cockpit.directory-toggle"]')).toHaveLength(2);
    expect(studio.querySelector('[data-testid="cockpit.session-where"]')).toBeNull();
    // Add project stays on the machine row
    expect(mac.querySelector('[data-testid="cockpit.add-project"]')).not.toBeNull();
    expect(JSON.parse(localStorage.getItem(VIEWS_KEY) ?? '{}')).toEqual({
      'name:mac': { organize: 'flat', sort: 'activity' },
    });
    // the menu closed with the choice
    expect(host.querySelector('[role="menu"]')).toBeNull();
  });

  it('sorts a flat machine by activity, creation or title, across its directories', async () => {
    const mixed = [
      meta('a', 'beta', 'notes', { updatedAt: 30, createdAt: 1 }),
      meta('b', 'alpha', 'app', { updatedAt: 10, createdAt: 2 }),
      meta('c', 'gamma', 'notes', { updatedAt: 20, createdAt: 3 }),
    ];
    await show({}, undefined, null, mixed);
    await pick('cockpit.organize-flat');
    expect(titles()).toEqual(['beta', 'gamma', 'alpha']);
    await pick('cockpit.sort-created');
    expect(titles()).toEqual(['gamma', 'alpha', 'beta']);
    await pick('cockpit.sort-title');
    expect(titles()).toEqual(['alpha', 'beta', 'gamma']);
  });

  it('sorts the sessions of a folder view too', async () => {
    const same = [
      meta('a', 'beta', 'notes', { updatedAt: 30, createdAt: 1 }),
      meta('b', 'alpha', 'notes', { updatedAt: 10, createdAt: 2 }),
    ];
    await show({}, undefined, null, same);
    expect(titles()).toEqual(['beta', 'alpha']);
    await pick('cockpit.sort-title');
    expect(titles()).toEqual(['alpha', 'beta']);
    await pick('cockpit.sort-created');
    expect(titles()).toEqual(['alpha', 'beta']);
  });

  it('starts from the saved view of each machine', async () => {
    localStorage.setItem(
      VIEWS_KEY,
      JSON.stringify({ 'name:studio': { organize: 'flat', sort: 'title' } }),
    );
    await show({}, undefined, null, two);
    expect(host.querySelectorAll('[data-testid="cockpit.session-where"]')).toHaveLength(2);
    expect(host.querySelectorAll('[data-testid="cockpit.directory-toggle"]')).toHaveLength(1);
  });

  it('keeps the menu on an offline machine', async () => {
    await show(
      {},
      undefined,
      null,
      two.map((s) => ({ ...s, ownerConnected: false })),
    );
    expect(host.querySelectorAll('[data-testid="cockpit.machine-menu"]')).toHaveLength(2);
    await act(async () => menu('cockpit.machine-menu').click());
    expect(host.querySelector('[role="menu"]')).not.toBeNull();
  });
});
