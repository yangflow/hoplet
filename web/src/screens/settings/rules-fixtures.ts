import {
  BUILTIN_PERMISSION_RULES,
  type DeviceInfo,
  type PermissionRuleEntry,
  type PermissionRuleListResponse,
} from '@hoplet/shared';
import { emptyForm, type RuleForm } from '../../lib/permission-rules.ts';
import type { RulesFixture } from './rules.tsx';

// Fixtures for /__states/settings/rules*: the screens of design/screens/rules.html. Nothing here is read by the real screen.

const NOW = Date.UTC(2026, 9, 8, 12, 0, 0);

function device(deviceId: string, name: string, os: string, connected: boolean): DeviceInfo {
  return {
    deviceId,
    name,
    connected,
    lastSeen: NOW,
    system: { os, arch: 'arm64' },
    providers: [],
    labels: [],
  };
}

const MACBOOK = device('dev-macbook', 'MacBook Pro', 'darwin', true);
const STUDIO = device('dev-studio', 'Studio', 'linux', true);

function own(
  id: string,
  over: Partial<PermissionRuleEntry> & Pick<PermissionRuleEntry, 'tool' | 'match' | 'action'>,
): PermissionRuleEntry {
  return {
    id,
    scope: 'global',
    note: '',
    enabled: true,
    builtin: false,
    createdAt: NOW - 86_400_000,
    updatedAt: NOW - 86_400_000,
    ...over,
  };
}

const MINE: PermissionRuleEntry[] = [
  own('r1', {
    tool: 'run',
    match: { command: 'git status' },
    action: 'allow',
    note: 'Looking is always fine',
  }),
  own('r2', {
    tool: 'run',
    match: { command: 'curl', pipedTo: ['sh', 'bash'] },
    action: 'ask',
    note: 'Never pipe a download into a shell unseen',
  }),
  own('r3', {
    tool: 'fetch',
    match: { domains: ['docs.example.com', '*.example.org'] },
    action: 'allow',
  }),
  own('r4', {
    tool: 'run',
    match: { command: 'pnpm test' },
    action: 'allow',
    note: 'Run the unit tests',
    scope: 'project',
    deviceId: MACBOOK.deviceId,
    projectPath: '/Users/dev/work/hoplet',
  }),
  own('r5', {
    tool: 'write',
    match: { paths: ['{workspace}/migrations/**'] },
    action: 'deny',
    scope: 'project',
    deviceId: MACBOOK.deviceId,
    projectPath: '/Users/dev/work/hoplet',
    enabled: false,
  }),
  own('r6', {
    tool: 'mcp:github/create_issue',
    match: {},
    action: 'ask',
    scope: 'project',
    deviceId: STUDIO.deviceId,
    projectPath: '/home/dev/api',
  }),
];

const BUILTIN: PermissionRuleEntry[] = BUILTIN_PERMISSION_RULES.map((r) => ({ ...r }));

const LOCAL = [
  {
    deviceId: STUDIO.deviceId,
    rules: [
      {
        id: 'local.1',
        tool: 'read',
        match: { paths: ['/srv/secrets/**'] },
        action: 'deny' as const,
        scope: 'agent' as const,
        note: 'Production secrets',
        enabled: true as const,
        builtin: false as const,
      },
    ],
  },
];

function list(
  rules: PermissionRuleEntry[],
  local: PermissionRuleListResponse['local'],
): PermissionRuleListResponse {
  return { rules: [...BUILTIN, ...rules], ...(local ? { local } : {}) };
}

export function rulesFixture(
  over: {
    own?: PermissionRuleEntry[];
    form?: Partial<RuleForm>;
    builtinOpen?: boolean;
    confirm?: string;
    toast?: string;
    local?: boolean;
  } = {},
): RulesFixture {
  return {
    list: list(over.own ?? MINE, over.local === false ? [] : LOCAL),
    devices: [MACBOOK, STUDIO],
    projects: {
      [MACBOOK.deviceId]: ['/Users/dev/work/hoplet', '/Users/dev/work/site'],
      [STUDIO.deviceId]: ['/home/dev/api'],
    },
    ...(over.form ? { form: { ...emptyForm(), ...over.form } } : {}),
    ...(over.builtinOpen ? { builtinOpen: true } : {}),
    ...(over.confirm ? { confirm: over.confirm } : {}),
    ...(over.toast ? { toast: over.toast } : {}),
  };
}
