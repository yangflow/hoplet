// @vitest-environment jsdom
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { HooksSection } from '../src/screens/settings/hooks.tsx';
import { hooksStates } from '../src/screens/settings/hooks-fixtures.ts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let host: HTMLDivElement;

const q = (id: string) => host.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const click = async (el: Element | null | undefined) => {
  await act(async () => {
    (el as HTMLElement).click();
  });
};
const show = async (name: keyof typeof hooksStates) => {
  await act(async () => {
    root.render(createElement(HooksSection, { fixture: hooksStates[name]() }));
  });
};

beforeEach(() => {
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe('Settings > Hooks', () => {
  it('lists the webhooks and the scripts of the machines', async () => {
    await show('list');
    expect(q('settings.hooks')).not.toBeNull();
    expect(q('settings.hooks.row.hook-slack')).not.toBeNull();
    expect(q('settings.hooks.script.script:dev-macbook:notify-done')).not.toBeNull();
    expect(q('settings.hooks.detail.hook-slack')).toBeNull();
  });

  it('opens a row to show its deliveries', async () => {
    await show('list');
    await click(q('settings.hooks.open.hook-slack'));
    expect(q('settings.hooks.detail.hook-slack')).not.toBeNull();
    expect(q('settings.hooks.deliveries')).not.toBeNull();
  });

  it('offers a new secret for a hook that lost its own', async () => {
    await show('needsSecret');
    expect(q('settings.hooks.rotate-lost')).not.toBeNull();
  });

  it('opens the editor for a new webhook', async () => {
    await show('add');
    expect(q('settings.hooks.editor')).not.toBeNull();
    expect(q('settings.hooks.label')).not.toBeNull();
  });

  it('shows a secret once, with a way to copy it', async () => {
    await show('secret');
    expect(q('settings.hooks.secret')?.textContent).toMatch(/^whsec_/);
    expect(q('settings.hooks.copy')).not.toBeNull();
  });

  it('asks before rotating a secret', async () => {
    await show('rotateConfirm');
    expect(q('settings.hooks.confirm')).not.toBeNull();
  });

  it('reports a refused test', async () => {
    await show('testBad');
    expect(q('settings.hooks.test-note')?.textContent).toMatch(/blocked-address/);
  });

  it('shows no webhook row when none exists', async () => {
    await show('empty');
    expect(q('settings.hooks.row.hook-slack')).toBeNull();
  });
});
