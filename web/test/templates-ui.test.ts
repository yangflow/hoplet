// @vitest-environment jsdom
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { TemplatesSection } from '../src/screens/settings/templates.tsx';
import {
  DEPS,
  FIX,
  IMPORT_FILE,
  NOTES,
  REVIEW,
  STUDIO,
  templateList,
  templatesFixture,
} from '../src/screens/settings/templates-fixtures.ts';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let host: HTMLDivElement;

const q = (id: string) => host.querySelector<HTMLElement>(`[data-testid="${id}"]`);
const all = (id: string) => [...host.querySelectorAll<HTMLElement>(`[data-testid="${id}"]`)];
const click = async (el: Element | null | undefined) => {
  await act(async () => {
    (el as HTMLElement).click();
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
const page = async (over: Parameters<typeof templatesFixture>[0] = {}) => {
  await act(async () => {
    root.render(createElement(TemplatesSection, { fixture: templatesFixture(over) }));
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

describe('Settings > Templates', () => {
  it('lists a row per template with where it runs and its options', async () => {
    await page();
    expect(all('settings.templates.edit')).toHaveLength(4);
    const row = q(`settings.template.${REVIEW.id}`);
    expect(row?.textContent).toContain('Review a pull request');
    expect(row?.textContent).toContain('MacBook Pro');
    expect(row?.textContent).toContain('hoplet');
    expect(q(`settings.template.${REVIEW.id}`)?.querySelector('.tpo')?.textContent).toBe(
      'Opus 5.5 · Thinking · Manual',
    );
    expect(host.textContent).toContain(
      '4 of 100 templates. Placeholders are filled in when a session starts.',
    );
  });

  it('opens the template being edited as a form in place of the list, with Delete', async () => {
    await page({ editing: REVIEW.id });
    expect(q('settings.templates.form')).not.toBeNull();
    expect(host.querySelector('h1')?.textContent).toBe('Edit template');
    expect((q('settings.templates.name') as HTMLInputElement).value).toBe(REVIEW.name);
    expect(all('settings.templates.edit')).toHaveLength(0);
    expect(q('settings.templates.delete')).not.toBeNull();
    expect(q('settings.templates.placeholders')?.textContent).toContain('{{repo:hoplet}}');
  });

  it('saves a change and puts the row back', async () => {
    await page({ editing: REVIEW.id });
    await type(q('settings.templates.name'), 'Review a PR');
    await click(q('settings.templates.save'));
    expect(q('settings.templates.form')).toBeNull();
    expect(host.textContent).toContain('Review a PR');
    expect(q('toast')?.textContent).toContain('Saved “Review a PR”');
  });

  it('keeps the form open on Save while the name and prompt are empty, and has no Delete for a new one', async () => {
    await page();
    await click(q('settings.templates.add'));
    expect(q('settings.templates.delete')).toBeNull();
    await click(q('settings.templates.save'));
    expect(q('settings.templates.form')).not.toBeNull();
    expect(q('settings.templates.name-error')?.textContent).toBe('Give the template a name.');
    expect(q('settings.templates.text-error')?.textContent).toBe('Write the prompt.');
  });

  it('points at the placeholder that does not parse', async () => {
    await page();
    await click(q('settings.templates.add'));
    await type(q('settings.templates.name'), 'Broken');
    await type(q('settings.templates.text'), 'Review {{Pr}}');
    await click(q('settings.templates.save'));
    expect(q('settings.templates.text-error')?.textContent).toContain('Line 1, column 8');
    expect(q('settings.templates.form')).not.toBeNull();
  });

  it('refuses a name another template has', async () => {
    await page();
    await click(q('settings.templates.add'));
    await type(q('settings.templates.name'), FIX.name);
    await type(q('settings.templates.text'), 'Do it');
    await click(q('settings.templates.save'));
    expect(q('settings.templates.name-error')?.textContent).toContain('Choose another name');
  });

  it('adds a new template to the list', async () => {
    await page();
    await click(q('settings.templates.add'));
    await type(q('settings.templates.name'), 'Triage');
    await type(q('settings.templates.text'), 'Triage {{issue}}');
    expect(q('settings.templates.placeholders')?.textContent).toBe('{{issue}}');
    await click(q('settings.templates.save'));
    expect(q('settings.templates.form')).toBeNull();
    expect(all('settings.templates.edit')).toHaveLength(5);
  });

  it('duplicates a template into a new draft named as a copy', async () => {
    await page();
    await click(all('settings.templates.duplicate')[0]);
    expect((q('settings.templates.name') as HTMLInputElement).value).toBe(
      'Review a pull request (copy)',
    );
    expect(q('settings.templates.delete')).toBeNull();
    await click(q('settings.templates.save'));
    expect(all('settings.templates.edit')).toHaveLength(5);
  });

  it('deletes from a row', async () => {
    await page();
    await click(all('settings.templates.remove')[1]);
    expect(q(`settings.template.${FIX.id}`)).toBeNull();
    expect(all('settings.templates.edit')).toHaveLength(3);
    expect(q('toast')?.textContent).toContain('Deleted “Fix a failing test”');
  });

  it('deletes from the form', async () => {
    await page({ editing: NOTES.id });
    await click(q('settings.templates.delete'));
    expect(q('settings.templates.form')).toBeNull();
    expect(q(`settings.template.${NOTES.id}`)).toBeNull();
  });

  it('closes the form on Cancel without changing the list', async () => {
    await page({ editing: REVIEW.id });
    await type(q('settings.templates.name'), 'Other');
    await click(q('settings.templates.cancel'));
    expect(q('settings.templates.form')).toBeNull();
    expect(host.textContent).toContain('Review a pull request');
  });

  it('says what templates are when there are none, and exports nothing', async () => {
    await page({ list: templateList() });
    expect(q('settings.templates.empty')).not.toBeNull();
    expect(host.textContent).toContain('Write {{name}} where a value changes each time');
    expect(q('settings.templates.export')).toBeNull();
    await click(q('settings.templates.add'));
    expect(q('settings.templates.form')).not.toBeNull();
  });
});

describe('Settings > Templates > Import', () => {
  const importing = { file: IMPORT_FILE, fileName: 'hoplet-templates-2026-10-21.json' };

  it('previews what the file would add: renamed, new, and without a machine', async () => {
    await page({ devices: [STUDIO], list: templateList(FIX, DEPS), importing });
    const text = q('settings.templates.import')?.textContent ?? '';
    expect(text).toContain(
      'hoplet-templates-2026-10-21.json · v1 · 3 templates. Nothing starts and no credential is read.',
    );
    expect(text).toContain('studio / hoplet · Opus 5.5');
    expect(text).toContain('Fix a failing test (2)');
    expect(text).toContain('Renamed to “Fix a failing test (2)”');
    expect(text).toContain('New');
    expect(text).toContain('No machine named MacBook Pro here');
    expect(all('settings.templates.import-machine')).toHaveLength(1);
  });

  it('imports all entries and leaves the import view', async () => {
    await page({ devices: [STUDIO], list: templateList(FIX, DEPS), importing });
    await click(q('settings.templates.import-confirm'));
    expect(q('settings.templates.import')).toBeNull();
    expect(all('settings.templates.edit')).toHaveLength(5);
    expect(q('toast')?.textContent).toContain('Imported 3 templates');
  });

  it('leaves the list as it was on Cancel', async () => {
    await page({ devices: [STUDIO], list: templateList(FIX, DEPS), importing });
    await click(q('settings.templates.import-cancel'));
    expect(q('settings.templates.import')).toBeNull();
    expect(all('settings.templates.edit')).toHaveLength(2);
  });

  it('asks for no mapping when every machine of the file is here', async () => {
    await page({
      importing: {
        file: { v: 1, templates: IMPORT_FILE.templates.slice(0, 1) },
        fileName: 'a.json',
      },
    });
    expect(all('settings.templates.import-machine')).toHaveLength(0);
    expect(q('settings.templates.import')?.textContent).not.toContain('No machine named');
  });
});
