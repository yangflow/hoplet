import { describe, expect, it } from 'vitest';
import { ApiError } from '../src/lib/errors.ts';
import {
  baseName,
  draftOf,
  exportFileName,
  fill,
  newDraft,
  optionsOf,
  placeholderLabel,
  placeholdersOf,
  planImport,
  positionOf,
  providersOf,
  settingOf,
  templateProblem,
  templateReason,
  toBody,
  validateDraft,
} from '../src/lib/templates.ts';
import {
  DEPS,
  FIX,
  IMPORT_FILE,
  MACBOOK,
  REVIEW,
  STUDIO,
  TEMPLATE_DEVICES,
} from '../src/screens/settings/templates-fixtures.ts';

describe('template text helpers', () => {
  it('fills {name} and leaves what it has no value for', () => {
    expect(fill('Saved {name} ({n})', { name: 'a' })).toBe('Saved a ({n})');
  });

  it('turns an offset into a 1-based line and column', () => {
    expect(positionOf('ab\ncd {{', 6)).toEqual({ line: 2, column: 4 });
    expect(positionOf('{{', 0)).toEqual({ line: 1, column: 1 });
  });

  it('lists the placeholders in order, and none for a prompt that does not parse', () => {
    expect(placeholdersOf('Review {{pr}} in {{repo:hoplet}}').map(placeholderLabel)).toEqual([
      '{{pr}}',
      '{{repo:hoplet}}',
    ]);
    expect(placeholdersOf('{{Pr}}')).toEqual([]);
  });

  it('takes the last segment of a path', () => {
    expect(baseName('/home/mark/hoplet/')).toBe('hoplet');
    expect(baseName('C:\\src\\docs')).toBe('docs');
  });
});

describe('what the core refuses, at its field', () => {
  it('maps the name conflict and the machines to their fields', () => {
    expect(templateReason('name', {})).toMatchObject({ field: 'name' });
    expect(templateReason('machines', {})).toMatchObject({ field: 'machines' });
    expect(templateReason('something else', {})).toBeNull();
    expect(templateReason(undefined, {})).toBeNull();
  });

  it('says where a placeholder is wrong in the prompt', () => {
    const text = 'Line one\nReview {{Pr}}';
    const found = templateReason('text:syntax:16', { text });
    expect(found?.field).toBe('text');
    expect(found?.message).toContain('Line 2, column 8');
    expect(found?.message).toContain('{{Pr}}');
    const many = templateReason('text:too-many:0', { text });
    expect(many?.message).toContain('at most 20 placeholders');
  });

  it('names the template of an import file, and keeps it out of the prompt field', () => {
    const file = {
      v: 1 as const,
      templates: [
        { name: 'a', text: 'fine', provider: 'claude' as const },
        { name: 'b', text: 'x {{Bad}}', provider: 'claude' as const },
      ],
    };
    const found = templateReason('file.1.text:syntax:2', { file });
    expect(found?.field).toBe('form');
    expect(found?.message).toContain('Template 2, b.');
    expect(found?.message).toContain('Line 1, column 3');
  });

  it('reads an ApiError, a full list, and falls back to the error text', () => {
    const describe = () => 'boom';
    expect(templateProblem(new ApiError(400, 'invalid-request', 'name'), describe, {}).field).toBe(
      'name',
    );
    expect(templateProblem(new ApiError(413, 'too-large'), describe, {}).message).toContain(
      '100 templates',
    );
    expect(templateProblem(new Error('x'), describe, {})).toEqual({
      field: 'form',
      message: 'boom',
    });
  });
});

describe('the editor draft', () => {
  it('starts empty and for the claude provider', () => {
    expect(newDraft()).toMatchObject({
      id: null,
      name: '',
      provider: 'claude',
      configuration: 'claude',
    });
  });

  it('copies a template without its ID and with a name that says so', () => {
    expect(draftOf(REVIEW).id).toBe(REVIEW.id);
    const copy = draftOf(REVIEW, true);
    expect(copy.id).toBeNull();
    expect(copy.name).toBe('Review a pull request (copy)');
    expect(copy.config).toEqual(REVIEW.config);
  });

  it('sends only what is set', () => {
    expect(toBody({ ...newDraft(), name: ' a ', text: ' b ' })).toEqual({
      name: 'a',
      text: 'b',
      provider: 'claude',
    });
    expect(toBody(draftOf(REVIEW))).toMatchObject({
      deviceId: 'dev-macbook',
      projectPath: REVIEW.projectPath,
      config: REVIEW.config,
    });
    expect(
      toBody({ ...newDraft(), name: 'a', text: 'b', configuration: 'work' }).configuration,
    ).toBe('work');
  });

  it('checks the name and the prompt before the core is asked', () => {
    expect(validateDraft({ ...newDraft(), name: 'a', text: 'b' })).toBeNull();
    expect(validateDraft(newDraft())).toEqual({
      name: 'Give the template a name.',
      text: 'Write the prompt.',
    });
    expect(validateDraft({ ...newDraft(), name: 'x'.repeat(81), text: 'b' })?.name).toContain('80');
    expect(validateDraft({ ...newDraft(), name: 'a', text: 'x'.repeat(8001) })?.text).toContain(
      '8,000',
    );
    expect(validateDraft({ ...newDraft(), name: 'a', text: 'run {{Pr}}' })?.text).toContain(
      'Line 1, column 5',
    );
  });
});

describe('providers and options', () => {
  it('offers the configurations of the chosen machine, else of every machine once', () => {
    expect(providersOf(TEMPLATE_DEVICES, 'dev-studio')).toHaveLength(1);
    expect(providersOf(TEMPLATE_DEVICES, '')).toHaveLength(1);
    expect(providersOf(TEMPLATE_DEVICES, 'nope')).toEqual([]);
  });

  it('shows what the draft set, else the default of the configuration, else the driver', () => {
    const info = MACBOOK.providers[0];
    const [mode, model, thinking] = optionsOf(info);
    expect(optionsOf(info).map((o) => o.id)).toEqual(['mode', 'model', 'thinking', 'fast']);
    if (!mode || !model || !thinking) throw new Error('fixture');
    expect(settingOf(info, mode, {})).toBe('default');
    expect(settingOf(info, model, { model: 'sonnet' })).toBe('sonnet');
    expect(settingOf(info, thinking, { thinking: false })).toBe(false);
  });
});

describe('importing a file', () => {
  it('renames a taken name and says which machine each entry lands on', () => {
    const plan = planImport([FIX, DEPS], [STUDIO], IMPORT_FILE, {});
    expect(plan.items.map((i) => [i.name, i.renamed])).toEqual([
      ['Review a pull request', false],
      ['Fix a failing test (2)', true],
      ['Write release notes', false],
    ]);
    expect(plan.items[0]?.deviceId).toBe('dev-studio');
    expect(plan.items[2]).toMatchObject({ device: 'MacBook Pro' });
    expect(plan.items[2]?.deviceId).toBeUndefined();
  });

  it('uses the machine the user chose for a name that matches none', () => {
    const plan = planImport([], [STUDIO], IMPORT_FILE, { 'MacBook Pro': 'dev-studio' });
    expect(plan.items[2]?.deviceId).toBe('dev-studio');
  });
});

describe('export', () => {
  it('names the file by the local date', () => {
    expect(exportFileName(new Date(2026, 9, 5))).toBe('hoplet-templates-2026-10-05.json');
  });
});
