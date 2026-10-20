// @vitest-environment jsdom
import type { DeviceInfo, DeviceSessionResources } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { buildTree } from '../src/screens/cockpit/group.ts';
import { Sidebar } from '../src/screens/cockpit/sidebar/sidebar.tsx';
import { MachinesSection } from '../src/screens/settings/machines.tsx';

let root: Root;
let host: HTMLDivElement;

const GIB = 1024 ** 3;
const NOW = 1_000_000_000;
const sample = (memUsed: number, over: Partial<NonNullable<DeviceInfo['resources']>> = {}) => ({
  at: NOW,
  cpu: 40,
  memUsed: memUsed * GIB,
  memTotal: 64 * GIB,
  diskFree: 400e9,
  diskTotal: 1000e9,
  ...over,
});
const device = (id: string, name: string, over: Partial<DeviceInfo> = {}): DeviceInfo => ({
  deviceId: id,
  name,
  connected: true,
  lastSeen: NOW,
  system: { os: 'linux', arch: 'amd64' },
  providers: [],
  ...over,
});
const studio = device('dev-studio', 'studio', {
  resources: sample(60.2),
  warn: { memory: true, disk: false },
});
const quiet = device('dev-quiet', 'quiet', {
  resources: sample(10),
  warn: { memory: false, disk: false },
});
const silent = device('dev-silent', 'silent');

const procs: DeviceSessionResources[] = [
  {
    sessionId: 's-1',
    title: 'Small',
    projectPath: '/w/app',
    cpu: 2,
    rss: 1 * GIB,
    children: 1,
    history: [],
  },
  {
    sessionId: 's-2',
    title: 'Big',
    projectPath: '/w/notes',
    cpu: 30,
    rss: 9 * GIB,
    children: 3,
    history: [
      { at: NOW - 30_000, cpu: 30, rss: 1 * GIB },
      { at: NOW - 15_000, cpu: 30, rss: 2 * GIB },
      { at: NOW, cpu: 30, rss: 9 * GIB },
    ],
  },
  {
    sessionId: 's-3',
    title: null,
    projectPath: null,
    cpu: null,
    rss: 2 * GIB,
    children: 0,
    history: [],
  },
];

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

const q = (id: string) => host.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const qa = (id: string) => [...host.querySelectorAll<HTMLElement>(`[data-testid="${id}"]`)];

describe('the sidebar warning glyph', () => {
  async function show(devices: DeviceInfo[], opened: string[] = []) {
    await act(async () =>
      root.render(
        createElement(Sidebar, {
          groups: buildTree([], devices),
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
          onResources: (id: string) => opened.push(id),
        }),
      ),
    );
  }

  it('marks only a machine over a threshold, with a label that names the reading', async () => {
    await show([studio, quiet, silent]);
    const glyphs = qa('cockpit.resource-warning');
    expect(glyphs).toHaveLength(1);
    const g = glyphs[0] as HTMLElement;
    expect(g.getAttribute('role')).toBe('img');
    expect(g.getAttribute('aria-label')).toBe(
      'studio: memory 94% used · details in Settings › Machines',
    );
    expect(g.getAttribute('title')).toBe(g.getAttribute('aria-label'));
  });

  it('names both readings when memory and disk are over', async () => {
    await show([
      device('dev-x', 'x', {
        resources: sample(62, { diskFree: 50e9 }),
        warn: { memory: true, disk: true },
      }),
    ]);
    expect(q('cockpit.resource-warning')?.getAttribute('aria-label')).toContain(
      'memory 97% used, disk 5% free',
    );
  });

  it('offers Resources in the machine menu only for a machine that reports', async () => {
    const opened: string[] = [];
    await show([studio, silent], opened);
    // The machines list by name: silent, then studio.
    const [silentMenu, studioMenu] = qa('cockpit.machine-menu') as HTMLElement[];
    await act(async () => silentMenu?.click());
    expect(q('cockpit.machine-resources')).toBeNull();
    await act(async () => silentMenu?.click());
    await act(async () => studioMenu?.click());
    const item = q('cockpit.machine-resources');
    expect(item?.getAttribute('role')).toBe('menuitem');
    await act(async () => item?.click());
    expect(opened).toEqual(['dev-studio']);
  });
});

describe('Settings > Machines resources', () => {
  async function show(
    devices: DeviceInfo[],
    selected: string,
    sessions: DeviceSessionResources[] = procs,
    opened: string[] = [],
  ) {
    await act(async () =>
      root.render(
        createElement(MachinesSection, {
          fixture: {
            now: NOW + 6 * 3_600_000,
            devices,
            selected,
            resources: { samples: [], sessions, at: NOW },
            sessions: [],
          },
          onOpenSession: (id: string) => opened.push(id),
        }),
      ),
    );
  }

  it('shows a tab per machine and lists the sessions largest in memory first', async () => {
    await show([studio, quiet], 'dev-studio');
    expect(q('settings.machines.tabs')?.querySelectorAll('[role="tab"]')).toHaveLength(2);
    const rows = qa('settings.resources.session');
    expect(rows.map((r) => r.querySelector('.t1')?.textContent)).toEqual([
      'Big',
      'Untitled session',
      'Small',
    ]);
    expect(q('settings.resources.warning')?.textContent).toBe('Memory 94% used, above 90%');
  });

  it('opens a known session on click and Enter, and leaves an unknown one alone', async () => {
    const opened: string[] = [];
    await show([studio], 'dev-studio', procs, opened);
    const [big, unknown, small] = qa('settings.resources.session') as HTMLElement[];
    expect(big?.getAttribute('aria-label')).toBe('Open Big');
    expect(big?.getAttribute('tabindex')).toBe('0');
    await act(async () => big?.click());
    await act(async () =>
      small?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })),
    );
    expect(opened).toEqual(['s-2', 's-1']);
    expect(unknown?.hasAttribute('tabindex')).toBe(false);
    await act(async () => unknown?.click());
    expect(opened).toHaveLength(2);
  });

  it('greys an offline machine and says when it last reported', async () => {
    await show(
      [{ ...studio, connected: false, warn: { memory: false, disk: false } }],
      'dev-studio',
    );
    expect(q('settings.resources')?.classList.contains('stale')).toBe(true);
    expect(q('settings.resources.stale')?.textContent).toContain('Last reported');
    expect(qa('settings.resources.session')).toHaveLength(0);
    expect(q('settings.resources.warning')).toBeNull();
  });

  it('shows nothing resource related for a machine that reports none', async () => {
    await show([silent], 'dev-silent');
    expect(q('settings.resources')).toBeNull();
    expect(host.textContent).toContain('does not report resources');
  });
});
