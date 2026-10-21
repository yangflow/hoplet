import type {
  ConfigOption,
  DeviceInfo,
  ProviderInfo,
  Template,
  TemplateFile,
  TemplateListResponse,
} from '@hoplet/shared';
import { placeholdersOf } from '../../lib/templates.ts';
import type { TemplatesFixture } from './templates.tsx';

// Fixtures of Settings > Templates (design/screens/templates.html). Nothing here is read by the real screen.

const NOW = Date.UTC(2026, 9, 21, 9, 0, 0);
const DAY = 86_400_000;

export const TEMPLATE_PROJECT = '/Users/mark/github/repopro/hoplet';
const DOCS_PROJECT = '/Users/mark/github/repopro/docs';

const CLAUDE_OPTIONS: ConfigOption[] = [
  {
    type: 'select',
    id: 'mode',
    category: 'mode',
    name: 'Permission mode',
    currentValue: 'default',
    options: [
      { value: 'default', name: 'Manual' },
      { value: 'acceptEdits', name: 'Accept edits' },
      { value: 'plan', name: 'Plan' },
    ],
  },
  {
    type: 'select',
    id: 'model',
    category: 'model',
    name: 'Model',
    currentValue: 'opus',
    options: [
      { value: 'opus', name: 'Opus 5.5' },
      { value: 'sonnet', name: 'Sonnet 5.5' },
    ],
  },
  {
    type: 'boolean',
    id: 'thinking',
    category: 'thought_level',
    name: 'Thinking',
    currentValue: true,
  },
  {
    type: 'boolean',
    id: 'fast',
    category: 'model_config',
    name: 'Fast',
    currentValue: false,
  },
];

function claude(): ProviderInfo {
  return {
    provider: 'claude',
    configuration: {
      id: 'claude',
      name: 'Claude Code',
      extends: 'claude',
      enabled: true,
      env: [],
      defaults: {},
    },
    version: '2.1.0',
    tested: true,
    capabilities: {
      permissions: true,
      interrupt: true,
      images: true,
      steer: true,
      config: CLAUDE_OPTIONS,
    },
  };
}

function device(deviceId: string, name: string, os: string): DeviceInfo {
  return {
    deviceId,
    name,
    connected: true,
    lastSeen: NOW,
    system: { os, arch: 'arm64' },
    providers: [claude()],
    labels: [],
  };
}

export const MACBOOK = device('dev-macbook', 'MacBook Pro', 'darwin');
export const STUDIO = device('dev-studio', 'studio', 'linux');
export const TEMPLATE_DEVICES: DeviceInfo[] = [MACBOOK, STUDIO];

/** The directories each machine offers. */
export const TEMPLATE_PROJECTS: Record<string, string[]> = {
  'dev-macbook': [TEMPLATE_PROJECT, DOCS_PROJECT],
  'dev-studio': ['/home/mark/hoplet', '/home/mark/docs'],
};

function template(
  id: string,
  name: string,
  text: string,
  over: Partial<Template>,
  age: number,
): Template {
  return {
    id,
    name,
    text,
    provider: 'claude',
    placeholders: placeholdersOf(text),
    createdAt: NOW - age * DAY,
    updatedAt: NOW - age * DAY,
    ...over,
  };
}

export const REVIEW = template(
  'tpl-review',
  'Review a pull request',
  'Review pull request {{pr}} in {{repo:hoplet}}. Check the byte-cursor invariants and list the findings by severity.',
  {
    deviceId: 'dev-macbook',
    projectPath: TEMPLATE_PROJECT,
    config: { model: 'opus', mode: 'default', thinking: true },
  },
  30,
);
export const FIX = template(
  'tpl-fix',
  'Fix a failing test',
  'Run pnpm check and fix the first failing test. Do not change the test unless the test is wrong.',
  {
    deviceId: 'dev-studio',
    projectPath: '/home/mark/hoplet',
    config: { model: 'opus', mode: 'acceptEdits', thinking: true, fast: true },
  },
  21,
);
export const NOTES = template(
  'tpl-notes',
  'Write release notes',
  'Write release notes for {{version}} from the pull requests merged since {{since:the last tag}}.',
  {
    deviceId: 'dev-macbook',
    projectPath: TEMPLATE_PROJECT,
    config: { model: 'sonnet', mode: 'plan' },
  },
  12,
);
export const DEPS = template(
  'tpl-deps',
  'Update dependencies',
  'Update the dependencies that have a patch release and run pnpm check.',
  {
    deviceId: 'dev-studio',
    projectPath: '/home/mark/hoplet',
    config: { model: 'sonnet', mode: 'acceptEdits' },
  },
  5,
);

export function templateList(...templates: Template[]): TemplateListResponse {
  return { templates };
}

export function templatesFixture(over: Partial<TemplatesFixture> = {}): TemplatesFixture {
  return {
    list: templateList(REVIEW, FIX, NOTES, DEPS),
    devices: TEMPLATE_DEVICES,
    projects: TEMPLATE_PROJECTS,
    ...over,
  };
}

/** An export file from a host that has a machine this one does not. */
export const IMPORT_FILE: TemplateFile = {
  v: 1,
  templates: [
    {
      name: REVIEW.name,
      text: REVIEW.text,
      device: 'studio',
      project: '/home/mark/hoplet',
      provider: 'claude',
      config: { model: 'opus', mode: 'default', thinking: true },
    },
    {
      name: FIX.name,
      text: FIX.text,
      device: 'studio',
      project: '/home/mark/hoplet',
      provider: 'claude',
      config: { model: 'opus', mode: 'acceptEdits', thinking: true, fast: true },
    },
    {
      name: NOTES.name,
      text: NOTES.text,
      device: 'MacBook Pro',
      project: TEMPLATE_PROJECT,
      provider: 'claude',
      config: { model: 'sonnet', mode: 'plan' },
    },
  ],
};
