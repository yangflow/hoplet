// @vitest-environment jsdom
import type { Template } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { SessionTarget } from '../src/screens/cockpit/model.ts';
import { Palette } from '../src/screens/cockpit/palette.tsx';
import { NewMenu } from '../src/screens/cockpit/sidebar/new-menu.tsx';
import {
  type EntryChoice,
  entryChoices,
  menuDetail,
  planStart,
  sessionConfig,
} from '../src/screens/cockpit/template-entry.tsx';
import {
  FIX,
  MACBOOK,
  NOTES,
  REVIEW,
  TEMPLATE_PROJECT,
} from '../src/screens/settings/templates-fixtures.ts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const target = (online = true): SessionTarget => ({
  deviceName: MACBOOK.name,
  deviceId: MACBOOK.deviceId,
  online,
  system: MACBOOK.system ?? null,
  providers: MACBOOK.providers ?? [],
  dirs: [{ path: TEMPLATE_PROJECT, name: 'hoplet' }],
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
});

const mount = async (el: ReturnType<typeof createElement>) => {
  await act(async () => root.render(el));
};
const key = async (el: Element, k: string) => {
  await act(async () => {
    el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
  });
};

describe('planStart', () => {
  it('plans a template that names a known machine and project', () => {
    const plan = planStart(REVIEW, [target()]);
    expect(plan).toMatchObject({
      deviceId: MACBOOK.deviceId,
      machine: MACBOOK.name,
      path: TEMPLATE_PROJECT,
    });
    expect(plan?.options.length).toBeGreaterThan(0);
  });

  it('leaves it to the draft when the template names no machine, project or a known machine', () => {
    const open: Template = { ...NOTES, deviceId: undefined, projectPath: undefined };
    expect(planStart(open, [target()])).toBeNull();
    expect(planStart(REVIEW, [])).toBeNull();
  });
});

describe('entryChoices and menuDetail', () => {
  it('says where a template runs and how many fields it asks for', () => {
    const [review, fix] = entryChoices([REVIEW, FIX], [target()]);
    expect(review?.machine).toBe(MACBOOK.name);
    expect(review?.place).toBe(`${MACBOOK.name} / hoplet`);
    expect(review && menuDetail(review)).toContain('2 fields');
    expect(fix?.machine).toBe('Any machine');
    expect(fix?.place).toBe('Any machine');
    expect(fix && menuDetail(fix)).toBe('Any machine · Starts at once');
  });
});

describe('sessionConfig', () => {
  it('keeps the current values of selects and booleans', () => {
    const options = MACBOOK.providers?.[0]?.capabilities?.config ?? [];
    expect(sessionConfig(options)).toEqual({
      mode: 'default',
      model: 'opus',
      thinking: true,
      fast: false,
    });
  });
});

describe('NewMenu', () => {
  const choices = (): EntryChoice[] => entryChoices([REVIEW, FIX], [target()]);

  it('lists Blank session, the templates and Manage templates, and picks with the keyboard', async () => {
    const onBlank = vi.fn();
    const onPick = vi.fn();
    const onManage = vi.fn();
    await mount(
      createElement(NewMenu, { choices: choices(), onBlank, onPick, onManage, initialOpen: true }),
    );
    const menu = document.querySelector('[data-testid="cockpit.new-popover"]');
    expect(menu?.getAttribute('role')).toBe('menu');
    expect(document.querySelector('[data-testid="cockpit.new-blank"]')).not.toBeNull();
    const templates = [...document.querySelectorAll('[data-testid="cockpit.new-template"]')];
    expect(templates).toHaveLength(2);
    expect(templates[0]?.textContent).toContain(REVIEW.name);

    await act(async () => (templates[1] as HTMLElement).click());
    expect(onPick).toHaveBeenCalledWith(FIX);
  });

  it('opens Manage templates', async () => {
    const onManage = vi.fn();
    await mount(
      createElement(NewMenu, {
        choices: choices(),
        onBlank: vi.fn(),
        onPick: vi.fn(),
        onManage,
        initialOpen: true,
      }),
    );
    await act(async () =>
      (document.querySelector('[data-testid="cockpit.new-manage"]') as HTMLElement).click(),
    );
    expect(onManage).toHaveBeenCalled();
  });

  it('says so when there are no templates', async () => {
    await mount(
      createElement(NewMenu, {
        choices: [],
        onBlank: vi.fn(),
        onPick: vi.fn(),
        onManage: vi.fn(),
        initialOpen: true,
      }),
    );
    expect(document.body.textContent).toContain('No templates yet');
  });
});

describe('Palette templates', () => {
  const base = {
    sessions: [],
    searchFixture: { hits: [], scanning: false },
    onClose: vi.fn(),
    onSelectSession: vi.fn(),
    onSelectHit: vi.fn(),
    onNewSession: vi.fn(),
    onSettings: vi.fn(),
  };

  it('lists templates, starts the active one on Enter, and offers the save action', async () => {
    const onTemplate = vi.fn();
    const onSaveTemplate = vi.fn();
    await mount(
      createElement(Palette, {
        ...base,
        templates: entryChoices([REVIEW, FIX], [target()]),
        onTemplate,
        onSaveTemplate,
      }),
    );
    const options = [...document.querySelectorAll('[role="option"]')];
    expect(options[0]?.textContent).toContain(REVIEW.name);
    expect(document.body.textContent).toContain('to start');
    expect(options.some((o) => o.textContent?.includes('Save the current session'))).toBe(true);
    const input = document.querySelector('input[role="combobox"]') as HTMLElement;
    await key(input, 'Enter');
    expect(onTemplate).toHaveBeenCalledWith(REVIEW);
  });

  it('hides the save action when there is no session to save', async () => {
    await mount(
      createElement(Palette, {
        ...base,
        templates: entryChoices([REVIEW], [target()]),
        onTemplate: vi.fn(),
      }),
    );
    expect(document.body.textContent).not.toContain('Save the current session');
  });
});
