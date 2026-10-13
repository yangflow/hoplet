// @vitest-environment jsdom
import type { DeviceDirsResult } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it } from 'vitest';
import { AddProject, type AddProjectFixture } from '../src/screens/cockpit/sidebar/add-project.tsx';

const ROOT = '/home/me';
const listing = (path: string, parent: string | null, names: string[]): DeviceDirsResult => ({
  path,
  parent,
  root: ROOT,
  entries: names.map((name) => ({ name, git: false })),
  truncated: false,
});
const fixture: AddProjectFixture = {
  listings: {
    [ROOT]: listing(ROOT, null, ['projects']),
    [`${ROOT}/projects`]: listing(`${ROOT}/projects`, ROOT, ['repo']),
  },
  start: ROOT,
  candidates: [],
};

let root: Root;
let host: HTMLDivElement;
const q = (id: string) => host.querySelector(`[data-testid="${id}"]`) as HTMLElement;
const crumbs = () =>
  [...host.querySelectorAll('[data-testid="cockpit.add-crumb"]')].map((c) => c.textContent);
const key = (el: Element, k: string) =>
  act(async () => {
    el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
  });

beforeEach(async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
  await act(async () =>
    root.render(
      createElement(AddProject, {
        machines: [{ deviceId: 'dev', name: 'mac', online: true }],
        initialMachine: 'mac',
        onSubmit: async () => {},
        onClose: () => {},
        fixture,
      }),
    ),
  );
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

it('keeps the focus on the list when a row is pressed, so the keys work right after a folder opens', async () => {
  const list = q('cockpit.add-list');
  const row = q('cockpit.add-dir');
  // A row is an option of the list (aria-activedescendant): pressing it selects it but never takes the focus,
  // because the row is replaced when a folder opens and the focus would fall to the page with it.
  const press = new MouseEvent('mousedown', { bubbles: true, cancelable: true });
  await act(async () => {
    row.dispatchEvent(press);
    row.click();
  });
  expect(press.defaultPrevented).toBe(true);
  expect(document.activeElement).toBe(list);

  await key(list, 'Enter');
  expect(crumbs()).toEqual(['~', 'projects']);
  // Backspace at once, before any animation frame: the list still has the focus and goes up.
  await key(document.activeElement as Element, 'Backspace');
  expect(crumbs()).toEqual(['~']);
});
