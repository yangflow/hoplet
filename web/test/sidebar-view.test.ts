// @vitest-environment jsdom
import type { LiveSessionMeta } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot } from 'react-dom/client';
import { describe, expect, it } from 'vitest';
import { buildTree } from '../src/screens/cockpit/group.ts';
import {
  DEFAULT_VIEW,
  flatList,
  loadViews,
  saveViews,
  sortSessions,
  VIEWS_KEY,
  viewKey,
} from '../src/screens/cockpit/sidebar-view.ts';
import { TopBar } from '../src/screens/cockpit/topbar.tsx';

const memory = () => {
  const data = new Map<string, string>();
  return {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
  };
};

const meta = (
  id: string,
  title: string,
  device: string,
  dir: string,
  at: { updatedAt: number; createdAt: number },
): LiveSessionMeta =>
  ({
    sessionId: id,
    title,
    deviceName: device,
    projectPath: `/w/${dir}`,
    projectDir: `-w-${dir}`,
    provider: 'claude',
    ownerConnected: true,
    ...at,
  }) as LiveSessionMeta;

const list = [
  meta('a', 'beta', 'studio', 'hoplet', { updatedAt: 10, createdAt: 30 }),
  meta('b', 'alpha', 'mac', 'hoplet', { updatedAt: 30, createdAt: 10 }),
  meta('c', 'gamma', 'mac', 'notes', { updatedAt: 20, createdAt: 20 }),
];

describe('the saved machine views', () => {
  it('defaults to folders, newest activity first, for a machine with none', () => {
    expect(loadViews(memory())).toEqual({});
    expect(DEFAULT_VIEW).toEqual({ organize: 'folder', sort: 'activity' });
  });

  it('round-trips per machine and ignores values it does not know', () => {
    const store = memory();
    saveViews({ 'dev-a': { organize: 'flat', sort: 'title' } }, store);
    expect(loadViews(store)).toEqual({ 'dev-a': { organize: 'flat', sort: 'title' } });
    store.setItem(
      VIEWS_KEY,
      JSON.stringify({ 'dev-a': { organize: 'project', sort: 'created' }, 'dev-b': 7 }),
    );
    expect(loadViews(store)).toEqual({
      'dev-a': { organize: 'folder', sort: 'created' },
      'dev-b': DEFAULT_VIEW,
    });
    store.setItem(VIEWS_KEY, 'not json');
    expect(loadViews(store)).toEqual({});
  });

  it('keys a machine by its device ID, or by its name while it has none', () => {
    expect(viewKey({ deviceId: 'd1', name: 'mac' })).toBe('d1');
    expect(viewKey({ deviceId: null, name: 'mac' })).toBe('name:mac');
  });
});

describe('sorting and flattening', () => {
  const titles = (l: LiveSessionMeta[]) => l.map((s) => s.title);

  it('sorts by activity, creation or title', () => {
    expect(titles(sortSessions(list, 'activity'))).toEqual(['alpha', 'gamma', 'beta']);
    expect(titles(sortSessions(list, 'created'))).toEqual(['beta', 'gamma', 'alpha']);
    expect(titles(sortSessions(list, 'title'))).toEqual(['alpha', 'beta', 'gamma']);
  });

  it('flattens one machine in sort order, with the directory of each session', () => {
    const mac = buildTree(list, []).find((g) => g.name === 'mac');
    expect(mac).toBeDefined();
    const flat = flatList(mac as NonNullable<typeof mac>, 'title');
    expect(flat.map((p) => [p.session.title, p.dir])).toEqual([
      ['alpha', 'hoplet'],
      ['gamma', 'notes'],
    ]);
  });
});

describe('the top bar', () => {
  it('holds the title and the workspace toggle, without a machine crumb', async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    await act(async () =>
      root.render(
        createElement(TopBar, {
          session: list[1] as LiveSessionMeta,
          sidebarFolded: false,
          inspectorOpen: false,
          changes: 0,
          onShowSidebar: () => undefined,
          onToggleInspector: () => undefined,
        }),
      ),
    );
    expect(host.querySelector('h1')?.textContent).toBe('alpha');
    expect(host.textContent).not.toContain('mac');
    expect(host.querySelector('.crumb')).toBeNull();
    expect(host.querySelectorAll('button')).toHaveLength(1);
    await act(async () => root.unmount());
    host.remove();
  });
});
