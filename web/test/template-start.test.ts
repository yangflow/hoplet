import type { ProviderInfo, Template } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import { ApiError } from '../src/lib/errors.ts';
import {
  draftTemplateBody,
  draftText,
  fieldsLabel,
  isComplete,
  previewParts,
  type StartTarget,
  startIssues,
  startProblem,
  templateDetail,
} from '../src/lib/template-start.ts';
import {
  FIX,
  MACBOOK,
  NOTES,
  REVIEW,
  STUDIO,
  TEMPLATE_PROJECT,
} from '../src/screens/settings/templates-fixtures.ts';

const provider = MACBOOK.providers[0] as ProviderInfo;

const target = (over: Partial<StartTarget> = {}): StartTarget => ({
  deviceId: 'dev-macbook',
  deviceName: 'MacBook Pro',
  online: true,
  dirs: [TEMPLATE_PROJECT],
  path: TEMPLATE_PROJECT,
  provider,
  providers: [provider],
  config: { model: 'opus', mode: 'default', thinking: true },
  ...over,
});

describe('the prompt of a template', () => {
  it('previews the values, the defaults and, lacking both, the field name', () => {
    expect(previewParts(REVIEW.text, { pr: '#7' }).filter((p) => p.field)).toEqual([
      { text: '#7', field: 'pr' },
      { text: 'hoplet', field: 'repo' },
    ]);
    expect(previewParts('Fix {{issue}}', {})).toEqual([
      { text: 'Fix ' },
      { text: 'issue', field: 'issue' },
    ]);
    expect(previewParts('Fix {{issue}}', { issue: '' })[1]).toEqual({
      text: 'issue',
      field: 'issue',
    });
  });

  it('shows a text that does not parse as it is', () => {
    expect(previewParts('Broken {{', {})).toEqual([{ text: 'Broken {{' }]);
    expect(draftText('Broken {{')).toBe('Broken {{');
  });

  it('fills the draft as far as the values and defaults go and leaves the rest as placeholders', () => {
    expect(draftText(REVIEW.text)).toBe(
      'Review pull request {{pr}} in hoplet. Check the byte-cursor invariants and list the findings by severity.',
    );
    expect(draftText(NOTES.text, { version: 'v1' })).toBe(
      'Write release notes for v1 from the pull requests merged since the last tag.',
    );
  });

  it('is complete when every placeholder has a value or a default', () => {
    expect(isComplete(REVIEW.text, {})).toBe(false);
    expect(isComplete(REVIEW.text, { pr: '1' })).toBe(true);
    expect(isComplete(FIX.text, {})).toBe(true);
    expect(isComplete('Broken {{', {})).toBe(false);
  });
});

describe('what a template does not get', () => {
  it('lists nothing when the target fits', () => {
    expect(startIssues(REVIEW, target())).toEqual([]);
  });

  it('names an offline machine', () => {
    const issues = startIssues(REVIEW, target({ online: false }));
    expect(issues.map((i) => i.key)).toEqual(['machine']);
  });

  it('names a project the machine does not have and offers the one in use', () => {
    const issues = startIssues(REVIEW, target({ dirs: ['/Users/mark/other'] }));
    expect(issues).toHaveLength(1);
    expect(issues[0]?.key).toBe('project');
    expect(issues[0]?.fix).toEqual({ kind: 'accept', label: 'Use hoplet' });
  });

  it('does not judge the project of a template that runs on another machine', () => {
    expect(startIssues(FIX, target({ dirs: [] }))).toEqual([]);
  });

  it('names a machine that is not on this host', () => {
    const issues = startIssues(REVIEW, target(), new Set(), false);
    expect(issues.map((i) => i.key)).toEqual(['machine']);
    expect(issues[0]?.fix?.kind).toBe('accept');
  });

  it('names a configuration that is not enabled', () => {
    const issues = startIssues({ ...REVIEW, configuration: 'glm' }, target());
    expect(issues.map((i) => i.key)).toEqual(['configuration']);
  });

  it('names an option the machine does not offer, and a value it does not have, with a fix', () => {
    const none = startIssues({ ...REVIEW, config: { turbo: true } }, target());
    expect(none.map((i) => i.key)).toEqual(['option:turbo']);
    const value = startIssues(
      { ...REVIEW, config: { model: 'opus-4' } },
      target({ config: { model: 'opus-4' } }),
    );
    expect(value).toHaveLength(1);
    expect(value[0]?.key).toBe('option:model');
    expect(value[0]?.fix).toMatchObject({ kind: 'set', id: 'model', value: 'opus' });
  });

  it('stops naming an option once the user picked another value', () => {
    expect(
      startIssues(
        { ...REVIEW, config: { model: 'opus-4' } },
        target({ config: { model: 'sonnet' } }),
      ),
    ).toEqual([]);
  });

  it('drops what was accepted', () => {
    const accepted = new Set(['project']);
    expect(startIssues(REVIEW, target({ dirs: [] }), accepted)).toEqual([]);
  });
});

describe('a refused start', () => {
  it('is said in a sentence', () => {
    expect(startProblem(new ApiError(409, 'target-offline'))).toBe('The machine is offline.');
    expect(startProblem(new ApiError(400, 'invalid-request', 'values:missing:pr'))).toBe(
      'Fill in pr.',
    );
    expect(startProblem(new ApiError(400, 'invalid-request', 'projectPath'))).toBe(
      'The project is not on the machine.',
    );
    expect(startProblem(new ApiError(400, 'invalid-request', 'deviceId'))).toBe(
      'The machine of this template is not on this host.',
    );
    expect(startProblem(new ApiError(400, 'invalid-request', 'config'))).toBe(
      'The machine does not offer an option of this template.',
    );
  });

  it('is not guessed when the reason is unknown', () => {
    expect(startProblem(new ApiError(400, 'invalid-request', 'x'))).toBeNull();
    expect(startProblem(new Error('boom'))).toBeNull();
  });
});

describe('the picker rows', () => {
  it('counts the fields', () => {
    expect(fieldsLabel(FIX)).toBe('No fields');
    expect(fieldsLabel({ placeholders: [{ name: 'a' }] })).toBe('1 field');
    expect(fieldsLabel(REVIEW)).toBe('2 fields');
  });

  const machines = [MACBOOK, STUDIO].map((d) => ({
    deviceId: d.deviceId,
    deviceName: d.name,
    providers: d.providers,
  }));

  it('says where it runs, with which model, and how many fields', () => {
    expect(templateDetail(REVIEW, machines)).toBe('MacBook Pro / hoplet · Opus 5.5 · 2 fields');
  });

  it('says Any machine for a template that names none', () => {
    const open: Template = { ...NOTES, deviceId: undefined, projectPath: undefined, config: {} };
    expect(templateDetail(open, machines)).toBe('Any machine · 2 fields');
  });
});

describe('saving the draft', () => {
  const input = {
    name: 'Mine',
    text: 'Do {{x}}',
    deviceId: 'dev-macbook',
    path: TEMPLATE_PROJECT,
    provider: 'claude' as const,
    configuration: 'claude',
    config: { model: 'opus', mode: 'plan', thinking: true },
    modeId: 'mode',
  };

  it('keeps everything the user leaves on', () => {
    const body = draftTemplateBody({ ...input, keep: { where: true, options: true, mode: true } });
    expect(body).toMatchObject({
      name: 'Mine',
      text: 'Do {{x}}',
      deviceId: 'dev-macbook',
      projectPath: TEMPLATE_PROJECT,
      config: { model: 'opus', mode: 'plan', thinking: true },
    });
  });

  it('leaves out the place, the options and the mode when asked', () => {
    const body = draftTemplateBody({
      ...input,
      keep: { where: false, options: false, mode: false },
    });
    expect(body.deviceId).toBeUndefined();
    expect(body.projectPath).toBeUndefined();
    expect(body.config ?? {}).toEqual({});
  });

  it('keeps the options without the mode', () => {
    const body = draftTemplateBody({ ...input, keep: { where: true, options: true, mode: false } });
    expect(body.config).toEqual({ model: 'opus', thinking: true });
  });
});
