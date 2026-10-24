// @vitest-environment jsdom
import type { LiveSessionMeta } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { buildTree } from '../src/screens/cockpit/group.ts';
import { GROUPING_KEY } from '../src/screens/cockpit/sidebar/organize.ts';
import { Sidebar } from '../src/screens/cockpit/sidebar/sidebar.tsx';
import { TagEditor } from '../src/screens/cockpit/tag-editor.tsx';

let root: Root;
let host: HTMLDivElement;

const meta = (id: string, over: Partial<LiveSessionMeta> = {}): LiveSessionMeta =>
  ({
    sessionId: id,
    title: `title ${id}`,
    deviceName: 'studio',
    projectPath: '/w/app',
    projectDir: '-w-app',
    provider: 'claude',
    ownerConnected: true,
    updatedAt: 0,
    createdAt: 0,
    ...over,
  }) as LiveSessionMeta;

const REPO = 'github.com/acme/app';
const sessions = [
  meta('a', { tags: ['sync', 'bug'], repository: REPO }),
  meta('b', { tags: ['sync'], deviceName: 'mini', repository: REPO, projectPath: '/src/app' }),
  meta('c', { projectPath: '/w/notes', projectDir: '-w-notes' }),
];

async function show(extra: Record<string, unknown> = {}) {
  await act(async () =>
    root.render(
      createElement(Sidebar, {
        groups: buildTree(sessions, []),
        selected: null,
        runtime: {},
        now: 0,
        host: 'hoplet.local',
        live: true,
        newSessionOpen: false,
        onSelect: () => undefined,
        onNewSession: () => undefined,
        onAddProject: () => undefined,
        onSearch: () => undefined,
        onFold: () => undefined,
        onSettings: () => undefined,
        ...extra,
      }),
    ),
  );
}
const q = (id: string) => [...host.querySelectorAll<HTMLElement>(`[data-testid="${id}"]`)];
const click = async (el: HTMLElement | undefined) => {
  if (!el) throw new Error('missing');
  await act(async () => el.click());
};

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

describe('the tag strip', () => {
  it('lists the tags in use with their counts and filters by one at a time', async () => {
    await show();
    expect(q('cockpit.tag-chip').map((b) => b.textContent)).toEqual(['sync2', 'bug1']);
    expect(q('cockpit.session')).toHaveLength(3);
    await click(q('cockpit.tag-chip')[1]);
    expect(q('cockpit.session')).toHaveLength(1);
    expect(q('cockpit.tag-filter')[0]?.textContent).toContain('1 of 3 sessions · tagged bug');
    await click(q('cockpit.tag-chip')[0]);
    expect(q('cockpit.session')).toHaveLength(2);
    expect(q('cockpit.tag-chip')[0]?.getAttribute('aria-pressed')).toBe('true');
    await click(q('cockpit.tag-clear')[0]);
    expect(q('cockpit.session')).toHaveLength(3);
    expect(q('cockpit.tag-filter')).toHaveLength(0);
  });

  it('is absent when no session has a tag', async () => {
    await act(async () =>
      root.render(
        createElement(Sidebar, {
          groups: buildTree([meta('x')], []),
          selected: null,
          runtime: {},
          now: 0,
          host: 'hoplet.local',
          live: true,
          newSessionOpen: false,
          onSelect: () => undefined,
          onNewSession: () => undefined,
          onAddProject: () => undefined,
          onSearch: () => undefined,
          onFold: () => undefined,
          onSettings: () => undefined,
        }),
      ),
    );
    expect(q('cockpit.tag-strip')).toHaveLength(0);
  });
});

describe('the Group menu', () => {
  it('groups by project across machines and remembers the choice in the browser', async () => {
    await show();
    await click(q('cockpit.group')[0]);
    await click(q('cockpit.group-project')[0]);
    expect(localStorage.getItem(GROUPING_KEY)).toBe('project');
    const projects = q('cockpit.project');
    expect(projects).toHaveLength(2);
    expect(projects[0]?.querySelectorAll('[data-testid="cockpit.session"]')).toHaveLength(2);
    expect(projects[0]?.textContent).toContain('2 machines');
    expect(projects[1]?.textContent).toContain('no remote');
  });

  it('groups by last activity', async () => {
    await show({ grouping: 'time' });
    expect(q('cockpit.time-bucket').length).toBeGreaterThan(0);
    expect(q('cockpit.session')).toHaveLength(3);
  });
});

describe('TagEditor', () => {
  it('adds with Enter, removes the last with Backspace and closes with Esc', async () => {
    const onAdd = vi.fn();
    const onRemove = vi.fn();
    const onClose = vi.fn();
    await act(async () =>
      root.render(
        createElement(TagEditor, {
          tags: ['sync'],
          existing: [
            { tag: 'sync', count: 2 },
            { tag: 'release', count: 1 },
          ],
          initialQuery: 're',
          onAdd,
          onRemove,
          onClose,
        }),
      ),
    );
    expect(q('cockpit.tag-option').map((o) => o.textContent)).toEqual(['release1', 'Create “re”']);
    const input = q('cockpit.tag-input')[0] as HTMLInputElement;
    const key = (k: string) =>
      act(async () => {
        input.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }));
      });
    await key('Enter');
    expect(onAdd).toHaveBeenCalledWith('release');
    await key('Backspace');
    expect(onRemove).toHaveBeenCalledWith('sync');
    await key('Escape');
    expect(onClose).toHaveBeenCalledWith(true);
  });
});
