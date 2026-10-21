// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  filterChoices,
  type TemplateChoice,
} from '../src/screens/cockpit/composer/templates-menu.tsx';
import { states } from '../src/screens/cockpit/states.tsx';
import { FIX, NOTES, REVIEW } from '../src/screens/settings/templates-fixtures.ts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let host: HTMLDivElement;

const q = (id: string) => document.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const all = (id: string) => [...document.querySelectorAll<HTMLElement>(`[data-testid="${id}"]`)];
const show = async (state: string) => {
  const render = states[state];
  if (!render) throw new Error(`no state ${state}`);
  await act(async () => {
    root.render(render());
  });
};
const type = async (el: HTMLElement | null, value: string) => {
  const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement : HTMLInputElement;
  const set = Object.getOwnPropertyDescriptor(proto.prototype, 'value')?.set;
  await act(async () => {
    set?.call(el, value);
    el?.dispatchEvent(new Event('input', { bubbles: true }));
  });
};

beforeEach(() => {
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

describe('the picker filter', () => {
  const choices: TemplateChoice[] = [
    { template: REVIEW, detail: 'MacBook Pro / hoplet · 2 fields' },
    { template: FIX, detail: 'studio / hoplet · No fields' },
    { template: NOTES, detail: 'MacBook Pro / hoplet · 2 fields' },
  ];

  it('lists all of them for an empty query', () => {
    expect(filterChoices(choices, '  ')).toHaveLength(3);
  });

  it('matches every word against the name, the prompt and the detail', () => {
    expect(filterChoices(choices, 'release').map((c) => c.template.id)).toEqual(['tpl-notes']);
    expect(filterChoices(choices, 'studio test').map((c) => c.template.id)).toEqual(['tpl-fix']);
    expect(filterChoices(choices, 'macbook zzz')).toEqual([]);
  });
});

describe('the new-session composer', () => {
  it('opens the picker with the templates, then Save and Manage', async () => {
    await show('draft-templates');
    expect(q('cockpit.templates-menu')).not.toBeNull();
    expect(all('cockpit.templates-option')).toHaveLength(4);
    expect(q('cockpit.templates-save')).not.toBeNull();
    expect(q('cockpit.templates-manage')).not.toBeNull();
  });

  it('narrows the picker by what is typed and says when nothing is left', async () => {
    await show('draft-templates');
    await type(q('cockpit.templates-find'), 'dependencies');
    expect(all('cockpit.templates-option')).toHaveLength(1);
    await type(q('cockpit.templates-find'), 'nothing like it');
    expect(all('cockpit.templates-option')).toHaveLength(0);
    expect(q('cockpit.templates-empty')).not.toBeNull();
  });

  it('asks for the fields of a template, previews the prompt and starts only when it is complete', async () => {
    await show('template-fill');
    expect(q('cockpit.template-fill')).not.toBeNull();
    expect(q('cockpit.template-field.pr')).not.toBeNull();
    expect(q('cockpit.template-field.repo')).not.toBeNull();
    expect(q('cockpit.template-preview')?.textContent).toContain(
      'Review pull request #312 in hoplet',
    );
    expect((q('cockpit.template-start') as HTMLButtonElement).disabled).toBe(false);
    await type(q('cockpit.template-field.pr'), '');
    expect((q('cockpit.template-start') as HTMLButtonElement).disabled).toBe(true);
    expect(q('cockpit.template-preview')?.textContent).toContain('Review pull request pr in');
  });

  it('shows the applied template over the draft, with its text in the composer', async () => {
    await show('template-applied');
    expect(q('cockpit.template-bar')?.textContent).toContain('Write release notes');
    expect(q('cockpit.template-warning')).toBeNull();
    expect(q('cockpit.template-clear')).not.toBeNull();
  });

  it('warns about what the machine does not offer, with a one-click fix', async () => {
    await show('template-mismatch');
    const warning = q('cockpit.template-warning');
    expect(warning?.textContent).toContain('opus-4');
    expect(q('cockpit.template-fix')).not.toBeNull();
    await act(async () => q('cockpit.template-fix')?.click());
    expect(q('cockpit.template-warning')).toBeNull();
  });

  it('clears the template and its warning', async () => {
    await show('template-mismatch');
    await act(async () => q('cockpit.template-clear')?.click());
    expect(q('cockpit.template-bar')).toBeNull();
    expect(q('cockpit.template-warning')).toBeNull();
  });

  it('saves the draft: the name and the prompt are checked, three switches say what goes along', async () => {
    await show('template-save');
    expect(q('cockpit.template-save-dialog')).not.toBeNull();
    expect((q('cockpit.template-name') as HTMLInputElement).value).toBe('Review a pull request');
    expect((q('cockpit.template-text') as HTMLTextAreaElement).value).toContain('{{pr}}');
    for (const key of ['where', 'options', 'mode']) {
      expect(q(`cockpit.template-keep.${key}`)).not.toBeNull();
    }
    await type(q('cockpit.template-name'), '');
    await act(async () => q('cockpit.template-save-submit')?.click());
    expect(q('cockpit.template-name-error')).not.toBeNull();
    expect((q('cockpit.template-save-submit') as HTMLButtonElement).disabled).toBe(true);
  });
});
