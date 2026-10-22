import type {
  ConfigOption,
  DeviceInfo,
  DeviceInventory,
  DeviceResourceSample,
  DeviceSessionResources,
  LiveSessionMeta,
  NotifyPreferences,
  ProviderInfo,
  ResourceThresholds,
  WebhookInfo,
} from '@hoplet/shared';
import { type Budget, RESOURCES_INTERVAL_MS } from '@hoplet/shared';
import { type ReactNode, useEffect } from 'react';
import { endDate } from '../../lib/budgets.ts';
import { t } from '../../lib/i18n.ts';
import { type Appearance, applyText, DEFAULTS } from '../../lib/prefs.ts';
import type { AuditFixture } from './audit.tsx';
import * as auditFx from './audit-fixtures.ts';
import { type BudgetsFixture, fill } from './budgets.tsx';
import type { HooksFixture } from './hooks.tsx';
import { hooksStates } from './hooks-fixtures.ts';
import { type SectionId, Settings } from './index.tsx';
import type { MachinesFixture } from './machines.tsx';
import type { NotifyFixture, PushState } from './notifications.tsx';
import type { ProvidersFixture } from './providers.tsx';
import type { RulesFixture } from './rules.tsx';
import { rulesFixture } from './rules-fixtures.ts';
import type { TemplatesFixture } from './templates.tsx';
import {
  DEPS,
  FIX,
  IMPORT_FILE,
  STUDIO,
  templateList,
  templatesFixture,
} from './templates-fixtures.ts';
import { effectiveThresholds, readingWarn } from './thresholds-model.ts';
import type { TokensFixture } from './tokens.tsx';
import { filledForm, issuedFixture, tokensFixture } from './tokens-fixtures.ts';
import type { UsageFixture } from './usage.tsx';
import {
  B1,
  B2,
  B3,
  B4,
  B4_ALLOWED,
  B4_OK,
  BUDGET_DEVICES,
  BUDGET_PROJECTS,
  backfillingUsage,
  budgetList,
  customUsage,
  emptyUsage,
  usageFixture,
} from './usage-fixtures.ts';

// Fixtures for /__states/settings/<state>: every state of design/screens/settings.html.
// Nothing here is read by the real screen.

const NOW = Date.UTC(2026, 9, 8, 12, 0, 0);

function machine(
  deviceId: string,
  name: string,
  connected: boolean,
  lastSeen: number,
  os?: string,
): DeviceInfo {
  return {
    deviceId,
    name,
    connected,
    lastSeen,
    system: os ? { os, arch: 'amd64' } : null,
    providers: [],
    labels: [],
  };
}

const GIB = 1024 ** 3;
const GB = 1e9;

function reading(at: number, cpu: number, mem: [number, number], disk: [number, number]) {
  return {
    at,
    cpu,
    memUsed: Math.round(mem[0] * GIB),
    memTotal: Math.round(mem[1] * GIB),
    diskFree: Math.round(disk[0] * GB),
    diskTotal: Math.round(disk[1] * GB),
  } satisfies DeviceResourceSample;
}

/** An hour of readings in 48 steps, ending on `end`: the shape the design draws. */
function trend(seed: number, base: number, amp: number, end: number, n = 48): number[] {
  const a = Array.from({ length: n }, (_, i) =>
    Math.max(
      2,
      Math.min(
        99,
        Math.round(
          base +
            amp * (Math.sin(seed + i * 0.3) * 0.6 + Math.sin(seed * 2 + i * 0.85) * 0.4) +
            (end - base) * (i / (n - 1)) ** 4,
        ),
      ),
    ),
  );
  a[n - 1] = end;
  return a;
}

type Proc = {
  id: string;
  title: string;
  project: string;
  state: 'running' | 'waiting-permission' | 'idle';
  cpu: number;
  gb: number;
  children: number;
  shape: number[];
};

type Machine = {
  device: DeviceInfo;
  samples: DeviceResourceSample[];
  procs: Proc[];
};

/** A machine as the design reports it: its latest reading, an hour of CPU and memory, the session processes. */
function reported(
  base: DeviceInfo,
  last: { cpu: number; mem: [number, number]; disk: [number, number] },
  hc: number[],
  hm: number[],
  procs: Proc[],
  age = 0,
): Machine {
  const now = base.connected ? NOW - age : (base.lastSeen ?? NOW);
  const samples = hc.map((cpu, i) => {
    const step = (hc.length - 1 - i) * 75_000;
    const mem: [number, number] = [((hm[i] ?? 0) * last.mem[1]) / 100, last.mem[1]];
    return reading(now - step, cpu, mem, last.disk);
  });
  const sample = reading(now, last.cpu, last.mem, last.disk);
  const warn = base.connected
    ? {
        memory: sample.memUsed / sample.memTotal > 0.9,
        disk: sample.diskFree / sample.diskTotal < 0.1,
      }
    : { memory: false, disk: false };
  return {
    device: { ...base, resources: sample, warn },
    samples: [...samples.slice(0, -1), sample],
    procs,
  };
}

const OFFICE_BASE = machine('dev-office', 'office-mini', false, NOW - 6 * 3_600_000, 'windows');
const MACHINES_REPORTED: Machine[] = [
  reported(
    machine('dev-macbook', 'MacBook Pro', true, NOW, 'darwin'),
    { cpu: 12, mem: [20.2, 32], disk: [184, 994] },
    trend(1, 22, 14, 12),
    trend(2, 58, 6, 63),
    [
      {
        id: 'res-mac-0',
        title: 'Hand off sessions with snapshots',
        project: 'hoplet',
        state: 'running',
        cpu: 9,
        gb: 1.8,
        children: 3,
        shape: trend(7, 50, 20, 70),
      },
      {
        id: 'res-mac-1',
        title: 'Audit docs/api.md',
        project: 'notes',
        state: 'idle',
        cpu: 1,
        gb: 1.1,
        children: 2,
        shape: trend(8, 50, 5, 55),
      },
    ],
  ),
  reported(
    machine('dev-studio', 'studio', true, NOW, 'linux'),
    { cpu: 71, mem: [60.2, 64], disk: [412, 1000] },
    trend(3, 45, 25, 71),
    trend(4, 78, 8, 94),
    [
      {
        id: 'res-studio-0',
        title: '多设备远程同步 AI 会话项目',
        project: 'notes',
        state: 'waiting-permission',
        cpu: 6,
        gb: 31.4,
        children: 5,
        shape: trend(9, 55, 12, 90),
      },
      {
        id: 'res-studio-1',
        title: 'Fix flaky vitest on CI',
        project: 'hoplet',
        state: 'running',
        cpu: 38,
        gb: 12.8,
        children: 4,
        shape: trend(10, 45, 25, 60),
      },
      {
        id: 'res-studio-2',
        title: 'Migrate fixtures to Go',
        project: 'hoplet',
        state: 'running',
        cpu: 14,
        gb: 8.2,
        children: 2,
        shape: trend(11, 40, 20, 50),
      },
      {
        id: 'res-studio-3',
        title: 'Roadmap 0.4 planning',
        project: 'notes',
        state: 'idle',
        cpu: 1,
        gb: 3.1,
        children: 1,
        shape: trend(12, 50, 3, 52),
      },
    ],
  ),
  reported(
    OFFICE_BASE,
    { cpu: 8, mem: [6.5, 16], disk: [38, 256] },
    trend(5, 10, 6, 8),
    trend(6, 40, 4, 41),
    [],
  ),
];

/** The Machines section on one machine's tab, as the resources endpoint and the polls so far would fill it. */
function resourcesOf(
  selected: string,
  hovered?: string,
  thresholds?: ResourceThresholds,
): MachinesFixture {
  const devices = [
    ...MACHINES_REPORTED.map((m) => m.device),
    machine('dev-lab', 'lab-box', true, NOW),
  ].map((d) =>
    thresholds && d.connected && d.resources
      ? { ...d, warn: readingWarn(d.resources, effectiveThresholds(thresholds, d.deviceId)) }
      : d,
  );
  const picked = MACHINES_REPORTED.find((m) => m.device.deviceId === selected);
  const total = picked?.device.resources?.memTotal ?? 0;
  const sessions: DeviceSessionResources[] = (picked?.procs ?? []).map((p) => ({
    sessionId: p.id,
    title: p.title,
    projectPath: `/Users/me/code/${p.project}`,
    cpu: p.cpu,
    rss: Math.round(p.gb * GIB),
    children: p.children,
    history: p.shape.map((pct, i) => ({
      at: NOW - (p.shape.length - 1 - i) * RESOURCES_INTERVAL_MS,
      cpu: p.cpu,
      rss: (pct / 100) * total,
    })),
  }));
  const metas: LiveSessionMeta[] = (picked?.procs ?? []).map((p) => ({
    sessionId: p.id,
    projectPath: `/Users/me/code/${p.project}`,
    projectDir: `-Users-me-code-${p.project}`,
    deviceName: picked?.device.name ?? '',
    ownerDeviceId: selected,
    ownerEpoch: 0,
    title: p.title,
    provider: 'claude',
    createdAt: NOW - 86_400_000,
    updatedAt: NOW - 60_000,
    lineCount: 100,
    byteSize: 10_000,
    ownerConnected: true,
    runtime: { state: p.state },
  }));
  return {
    now: NOW,
    devices,
    selected,
    resources: {
      samples: picked?.samples ?? [],
      sessions,
      ...(picked?.device.resources ? { at: picked.device.resources.at } : {}),
    },
    sessions: metas,
    ...(hovered ? { hovered } : {}),
    ...(thresholds ? { thresholds } : {}),
  };
}

const tools = (list: Record<string, string>) =>
  Object.entries(list).map(([name, version]) => ({ name, version }));
const MIN = 60_000;

/** What each machine's agent reported, as the design's Capabilities group draws it. */
const INVENTORIES: Record<string, DeviceInventory> = {
  'dev-macbook': {
    at: NOW - 41 * MIN,
    toolchains: tools({
      git: '2.51.0',
      node: '24.9.0',
      pnpm: '10.17.1',
      npm: '11.6.0',
      bun: '1.3.2',
      python3: '3.13.7',
      go: '1.25.3',
      cargo: '1.90.0',
      rustc: '1.90.0',
      java: '25.0.1',
      gh: '2.80.0',
    }),
    gpus: [{ name: 'Apple M4 Max', memory: 32 * GIB }],
    repositories: [
      { path: '/Users/me/code/hoplet', origin: 'github.com/yangflow/hoplet' },
      { path: '/Users/me/code/notes', origin: 'github.com/acme/notes' },
    ],
  },
  'dev-studio': {
    at: NOW - 17 * MIN,
    toolchains: tools({
      git: '2.51.0',
      node: '24.9.0',
      pnpm: '10.17.1',
      npm: '11.6.0',
      python3: '3.13.7',
      go: '1.25.3',
      cargo: '1.90.0',
      rustc: '1.90.0',
      docker: '28.5.1',
      gh: '2.80.0',
    }),
    gpus: [{ name: 'NVIDIA GeForce RTX 4090', memory: 24 * GIB }],
    repositories: [
      { path: '/home/me/code/hoplet', origin: 'github.com/yangflow/hoplet' },
      { path: '/home/me/code/notes', origin: 'github.com/acme/notes' },
      { path: '/home/me/code/scratch', origin: null },
    ],
  },
  'dev-office': {
    at: NOW - 6 * 60 * MIN,
    toolchains: tools({ git: '2.51.0', node: '22.20.0', npm: '10.9.3' }),
    gpus: [],
    repositories: [{ path: 'C:\\code\\notes', origin: 'github.com/acme/notes' }],
  },
};
const LABELS: Record<string, string[]> = {
  'dev-macbook': ['laptop'],
  'dev-studio': ['gpu', 'release'],
  'dev-office': ['build-win'],
  'dev-lab': ['lab'],
};

/**
 * The Machines section with the Capabilities group: `online` brings office-mini up with a fresh report (the sparse
 * state); its offline report is the one from 6 h ago, greyed.
 */
function capabilitiesOf(
  selected: string,
  over: Pick<MachinesFixture, 'addingLabel' | 'removedLabel'> & { online?: boolean } = {},
): MachinesFixture {
  const { online, ...fixture } = over;
  const base = resourcesOf(selected);
  const devices = base.devices.map((d): DeviceInfo => {
    const inventory = INVENTORIES[d.deviceId];
    const up =
      online && d.deviceId === 'dev-office' && d.resources
        ? {
            connected: true,
            resources: { ...d.resources, at: NOW },
            warn: { memory: false, disk: false },
          }
        : {};
    return {
      ...d,
      ...up,
      labels: LABELS[d.deviceId] ?? [],
      ...(inventory
        ? { inventory: up.connected ? { ...inventory, at: NOW - 12 * MIN } : inventory }
        : {}),
    };
  });
  return { ...base, ...fixture, devices };
}

const STUDIO_2 = { deviceId: 'dev-studio-2', name: 'studio-2', token: 'fixture-token' };

function machines(mint?: { copied: boolean }, online = false): MachinesFixture {
  const base = resourcesOf('dev-macbook');
  return {
    ...base,
    devices: online
      ? [...base.devices, machine(STUDIO_2.deviceId, STUDIO_2.name, true, NOW)]
      : base.devices,
    ...(mint ? { mint: { result: STUDIO_2, copied: mint.copied } } : {}),
  };
}

const PROJECTS = ['/Users/me/code/hoplet', '/Users/me/code/billing-api', '/Users/me/code/dotfiles'];

const HOOKS: WebhookInfo[] = [
  {
    id: 'hook-ops',
    label: 'Ops channel',
    url: 'https://hooks.example.com/services/T0/B0/xxxx',
    hasSecret: true,
    enabled: true,
    events: ['permission', 'plan', 'turn-done'],
    createdAt: NOW - 3 * 86_400_000,
    lastDelivery: { at: NOW - 2 * 60_000, ok: true },
  },
  {
    id: 'hook-home',
    label: 'Home server',
    url: 'https://home.example.net/hoplet',
    hasSecret: false,
    enabled: true,
    events: ['permission', 'plan', 'failure'],
    createdAt: NOW - 86_400_000,
    lastDelivery: { at: NOW - 3_600_000, ok: false, error: 'timeout' },
  },
];

const BASE_PREFS: NotifyPreferences = {
  preset: 'needs-me-and-done',
  events: {},
  projects: {},
  quietHours: null,
};

function notify(
  over: Partial<NotifyFixture> = {},
  prefs: Partial<NotifyPreferences> = {},
): NotifyFixture {
  return {
    preferences: { ...BASE_PREFS, ...prefs },
    projects: PROJECTS,
    push: 'on' as PushState,
    webhooks: HOOKS,
    now: NOW,
    ...over,
  };
}

const select = (
  id: string,
  name: string,
  current: string,
  values: [string, string][],
): ConfigOption => ({
  type: 'select',
  id,
  name,
  currentValue: current,
  options: values.map(([value, label]) => ({ value, name: label })),
});

const CLAUDE_CONFIG: ConfigOption[] = [
  select('mode', 'Permission mode', 'default', [['default', 'Manual']]),
  select('model', 'Model', 'default', [['default', 'Default model']]),
  { type: 'boolean', id: 'thinking', name: 'Thinking', currentValue: true },
  { type: 'boolean', id: 'fast', name: 'Fast', currentValue: false },
];

const CODEX_CONFIG: ConfigOption[] = [
  select('mode', 'Mode', 'workspace-write', [['workspace-write', 'Workspace write']]),
  select('model', 'Model', 'default', [['default', 'Default model']]),
  select('effort', 'Effort', 'default', [['default', 'Default effort']]),
  select('collaboration', 'Collaboration', 'default', [['default', 'Default']]),
];

type Over = {
  id?: string;
  name?: string;
  enabled?: boolean;
  command?: string;
  env?: { name: string; set: boolean }[];
};

function announced(
  provider: ProviderInfo['provider'],
  version: string,
  tested: boolean,
  over: Over = {},
): ProviderInfo {
  const builtIn = over.id === undefined;
  return {
    provider,
    configuration: {
      id: over.id ?? provider,
      name: over.name ?? (provider === 'claude' ? 'Claude Code' : 'Codex'),
      extends: provider,
      enabled: over.enabled ?? true,
      ...(over.command ? { command: over.command } : {}),
      env: over.env ?? [],
      defaults: {},
    },
    ...(builtIn ? { version, tested } : {}),
    capabilities: {
      permissions: true,
      interrupt: true,
      images: true,
      steer: true,
      config: provider === 'claude' ? CLAUDE_CONFIG : CODEX_CONFIG,
    },
  };
}

/** The configurations of one machine as the mockup draws them: the built-ins, GLM with its own login and DeepSeek without its key. */
function configurations(version: string, tested: boolean): ProviderInfo[] {
  return [
    announced('claude', version, tested),
    announced('claude', version, tested, {
      id: 'glm',
      name: 'GLM',
      env: [
        { name: 'ANTHROPIC_BASE_URL', set: true },
        { name: 'ANTHROPIC_DEFAULT_SONNET_MODEL', set: true },
        { name: 'ANTHROPIC_AUTH_TOKEN', set: true },
        { name: 'CLAUDE_CONFIG_DIR', set: true },
      ],
    }),
    announced('claude', version, tested, {
      id: 'deepseek',
      name: 'DeepSeek',
      command: '/opt/deepseek/bin/claude',
      env: [
        { name: 'ANTHROPIC_BASE_URL', set: true },
        { name: 'ANTHROPIC_MODEL', set: true },
        { name: 'ANTHROPIC_AUTH_TOKEN', set: false },
      ],
    }),
    announced('codex', '0.124.0', true, { enabled: false }),
  ];
}

function online(
  deviceId: string,
  name: string,
  providers: ProviderInfo[],
  os?: string,
): DeviceInfo {
  return { ...machine(deviceId, name, true, NOW, os), providers };
}

const MINUTE = 60_000;
const HOUR = 3_600_000;

function used(
  n: number,
  deviceId: string,
  deviceName: string,
  provider: 'claude' | 'codex',
  age: number,
): LiveSessionMeta[] {
  return Array.from({ length: n }, (_, i) => ({
    sessionId: `${provider}-${deviceId}-${i}`,
    projectPath: '/Users/me/code/hoplet',
    projectDir: '-Users-me-code-hoplet',
    deviceName,
    ownerDeviceId: deviceId,
    ownerEpoch: 0,
    title: `Session ${i + 1}`,
    provider,
    createdAt: NOW - 30 * 86_400_000,
    updatedAt: NOW - age - i * HOUR,
    lineCount: 100,
    byteSize: 10_000,
    ownerConnected: true,
    runtime: { state: 'idle' as const },
  }));
}

const PROVIDER_SESSIONS: LiveSessionMeta[] = [
  ...used(4, 'dev-macbook', 'MacBook Pro', 'claude', 5 * MINUTE),
  ...used(2, 'dev-macbook', 'MacBook Pro', 'codex', 3 * HOUR),
  ...used(7, 'dev-studio', 'studio', 'claude', 24 * HOUR),
  ...used(1, 'dev-studio', 'studio', 'codex', 48 * HOUR),
];

function providers(
  selected: string | undefined,
  over: {
    offline?: boolean;
    none?: boolean;
    empty?: boolean;
    open?: string[];
    edit?: ProvidersFixture['edit'];
    add?: string;
  } = {},
): ProvidersFixture {
  const devices = over.empty
    ? []
    : [
        online('dev-macbook', 'MacBook Pro', configurations('2.1.42', true), 'darwin'),
        online('dev-studio', 'studio', configurations('2.2.0', false), 'linux'),
        ...(over.offline
          ? [machine('dev-office', 'office-mini', false, NOW - 3 * HOUR, 'windows')]
          : []),
        ...(over.none ? [online('dev-nas', 'nas-box', [])] : []),
      ];
  const listing = selected === undefined && !over.empty && !over.edit && !over.add;
  const open = over.open ?? (listing ? ['dev-macbook/deepseek'] : []);
  return {
    devices,
    sessions: PROVIDER_SESSIONS,
    now: NOW,
    ...(selected ? { selected } : {}),
    open,
    ...(over.edit ? { edit: over.edit } : {}),
    ...(over.add ? { add: over.add } : {}),
  };
}

const DEFAULT_THRESHOLDS: ResourceThresholds = { default: { memory: 90, disk: 10 }, machines: {} };

const GLM = 'dev-macbook/glm';

/** The form of GLM the mockup draws: renamed, with Thinking turned off. */
function glmEdit(status: 'idle' | 'saving' | 'error'): NonNullable<ProvidersFixture['edit']> {
  return {
    key: GLM,
    name: 'GLM Coding Plan',
    enabled: true,
    values: { thinking: false },
    status,
    ...(status === 'error'
      ? {
          error:
            'MacBook Pro rejected the change: glm no longer exists on the machine. Its list was refreshed; nothing was saved.',
        }
      : {}),
  };
}

/** The Budgets tab with these budgets; an object among them changes the fixture (the form that starts open, a toast). */
function budgetsPage(...args: (Budget | Partial<BudgetsFixture>)[]): UsageFixture {
  const budgets = args.filter((a): a is Budget => 'scope' in a);
  const over = args.find((a): a is Partial<BudgetsFixture> => !('scope' in a)) ?? {};
  return usageFixture({
    tab: 'budgets',
    budgets: {
      list: budgetList(...budgets),
      projects: BUDGET_PROJECTS,
      devices: BUDGET_DEVICES,
      ...over,
    },
  });
}

/** The page with its text choices in effect, the way the app applies them; the theme is left to the URL. */
function Scene(props: {
  section: SectionId;
  appearance?: Partial<Appearance>;
  machines?: MachinesFixture;
  notify?: NotifyFixture;
  providers?: ProvidersFixture;
  rules?: RulesFixture;
  hooks?: HooksFixture;
  audit?: AuditFixture;
  usage?: UsageFixture;
  templates?: TemplatesFixture;
  tokens?: TokensFixture;
}) {
  const appearance = { ...DEFAULTS, ...props.appearance };
  useEffect(() => {
    applyText(appearance);
  });
  return (
    <Settings
      section={props.section}
      onSection={() => undefined}
      onBack={() => undefined}
      appearance={appearance}
      onAppearance={() => undefined}
      {...(props.machines ? { machines: props.machines } : {})}
      {...(props.notify ? { notify: props.notify } : {})}
      {...(props.providers ? { providers: props.providers } : {})}
      {...(props.rules ? { rules: props.rules } : {})}
      {...(props.hooks ? { hooks: props.hooks } : {})}
      {...(props.audit ? { audit: props.audit } : {})}
      {...(props.usage ? { usage: props.usage } : {})}
      {...(props.templates ? { templates: props.templates } : {})}
      {...(props.tokens ? { tokens: props.tokens } : {})}
    />
  );
}

export const states: Record<string, () => ReactNode> = {
  settings: () => <Scene section="appearance" />,
  'settings-text': () => (
    <Scene
      section="appearance"
      appearance={{
        uiFont: 'system',
        codeFont: 'system',
        size: 'large',
        wide: true,
        ligatures: false,
      }}
    />
  ),
  'settings-text-geist': () => (
    <Scene
      section="appearance"
      appearance={{ uiFont: 'geist', codeFont: 'geist-mono', size: 'small' }}
    />
  ),
  'settings-machines': () => <Scene section="machines" machines={machines()} />,
  mint: () => <Scene section="machines" machines={machines({ copied: false })} />,
  'mint-waiting': () => <Scene section="machines" machines={machines({ copied: true })} />,
  'mint-online': () => <Scene section="machines" machines={machines({ copied: true }, true)} />,
  'settings-resources': () => (
    <Scene section="machines" machines={resourcesOf('dev-studio', 'res-studio-1')} />
  ),
  'settings-resources-offline': () => (
    <Scene section="machines" machines={resourcesOf('dev-office')} />
  ),
  'settings-thresholds': () => (
    <Scene
      section="machines"
      machines={resourcesOf('dev-studio', 'res-studio-1', DEFAULT_THRESHOLDS)}
    />
  ),
  'settings-thresholds-default': () => (
    <Scene
      section="machines"
      machines={{
        ...resourcesOf('dev-studio', 'res-studio-1', DEFAULT_THRESHOLDS),
        editDefault: true,
      }}
    />
  ),
  'settings-thresholds-custom': () => (
    <Scene
      section="machines"
      machines={resourcesOf('dev-studio', 'res-studio-1', {
        ...DEFAULT_THRESHOLDS,
        machines: { 'dev-studio': { memory: 97, disk: 10 } },
      })}
    />
  ),
  'settings-capabilities': () => (
    <Scene section="machines" machines={capabilitiesOf('dev-studio')} />
  ),
  'settings-capabilities-mac': () => (
    <Scene section="machines" machines={capabilitiesOf('dev-macbook')} />
  ),
  'settings-capabilities-label-add': () => (
    <Scene
      section="machines"
      machines={capabilitiesOf('dev-studio', { addingLabel: 'ci-runner' })}
    />
  ),
  'settings-capabilities-label-remove': () => (
    <Scene
      section="machines"
      machines={(() => {
        const m = capabilitiesOf('dev-studio', { removedLabel: 'release' });
        return {
          ...m,
          devices: m.devices.map((d) =>
            d.deviceId === 'dev-studio' ? { ...d, labels: ['gpu'] } : d,
          ),
        };
      })()}
    />
  ),
  'settings-capabilities-label-invalid': () => (
    <Scene section="machines" machines={capabilitiesOf('dev-studio', { addingLabel: 'GPU box' })} />
  ),
  'settings-capabilities-sparse': () => (
    <Scene section="machines" machines={capabilitiesOf('dev-office', { online: true })} />
  ),
  'settings-capabilities-offline': () => (
    <Scene section="machines" machines={capabilitiesOf('dev-office')} />
  ),
  'settings-capabilities-none': () => (
    <Scene section="machines" machines={capabilitiesOf('dev-lab')} />
  ),
  'settings-providers': () => <Scene section="providers" providers={providers(undefined)} />,
  'settings-providers-edit': () => (
    <Scene section="providers" providers={providers(undefined, { edit: glmEdit('idle') })} />
  ),
  'settings-providers-saving': () => (
    <Scene section="providers" providers={providers(undefined, { edit: glmEdit('saving') })} />
  ),
  'settings-providers-error': () => (
    <Scene section="providers" providers={providers(undefined, { edit: glmEdit('error') })} />
  ),
  'settings-providers-add': () => (
    <Scene section="providers" providers={providers(undefined, { add: 'minimax' })} />
  ),
  'settings-providers-offline': () => (
    <Scene section="providers" providers={providers('dev-office', { offline: true })} />
  ),
  'settings-providers-none': () => (
    <Scene section="providers" providers={providers('dev-nas', { none: true })} />
  ),
  'settings-providers-empty': () => (
    <Scene section="providers" providers={providers(undefined, { empty: true })} />
  ),
  'settings-notify': () => (
    <Scene
      section="notifications"
      notify={notify(
        {},
        { quietHours: { start: '22:00', end: '08:00', timeZone: 'Europe/Berlin' } },
      )}
    />
  ),
  'settings-notify-overrides': () => (
    <Scene
      section="notifications"
      notify={notify(
        { overridesOpen: true, scope: PROJECTS[1] as string },
        {
          events: { 'turn-done': { channels: ['push'] } },
          projects: {
            [PROJECTS[1] as string]: {
              preset: 'needs-me',
              events: { failure: { channels: ['webhook'] } },
            },
          },
        },
      )}
    />
  ),
  'settings-notify-off': () => (
    <Scene
      section="notifications"
      notify={notify({ push: 'off', webhooks: [] }, { preset: 'off' })}
    />
  ),
  'settings-notify-blocked': () => (
    <Scene section="notifications" notify={notify({ push: 'blocked' })} />
  ),
  'notify-hook-new': () => <Scene section="notifications" notify={notify({ editing: 'new' })} />,
  'notify-hook': () => (
    <Scene
      section="notifications"
      notify={notify({
        editing: 'hook-ops',
        testResult: { ok: true, text: 'Test delivered' },
      })}
    />
  ),
  'settings-usage': () => <Scene section="usage" usage={usageFixture({ hover: 3 })} />,
  'settings-usage-session': () => (
    <Scene section="usage" usage={usageFixture({ group: 'session' })} />
  ),
  'settings-usage-machine': () => (
    <Scene section="usage" usage={usageFixture({ group: 'machine' })} />
  ),
  'settings-usage-custom': () => <Scene section="usage" usage={usageFixture(customUsage())} />,
  'settings-usage-tokens-only': () => (
    <Scene section="usage" appearance={{ tokensOnly: true }} usage={usageFixture()} />
  ),
  'settings-usage-unknown-model': () => (
    <Scene section="usage" usage={usageFixture({ group: 'model' })} />
  ),
  'settings-usage-empty': () => <Scene section="usage" usage={usageFixture(emptyUsage())} />,
  'settings-usage-backfill': () => (
    <Scene section="usage" usage={usageFixture(backfillingUsage())} />
  ),
  'settings-prices': () => <Scene section="usage" usage={usageFixture({ tab: 'prices' })} />,
  'settings-prices-edit': () => (
    <Scene section="usage" usage={usageFixture({ tab: 'prices', editing: 'claude-sonnet-4-5' })} />
  ),
  'settings-prices-tokens-only': () => (
    <Scene
      section="usage"
      appearance={{ tokensOnly: true }}
      usage={usageFixture({ tab: 'prices' })}
    />
  ),
  budgets: () => <Scene section="usage" usage={budgetsPage(B1, B2, B3, B4_OK)} />,
  'budgets-pause': () => <Scene section="usage" usage={budgetsPage(B1, B2, B3, B4)} />,
  'budgets-allowed': () => (
    <Scene
      section="usage"
      usage={budgetsPage(B1, B2, B3, B4_ALLOWED, {
        toast: fill(t('{name} is allowed until {end}'), {
          name: B4_ALLOWED.name,
          end: endDate(B4_ALLOWED),
        }),
      })}
    />
  ),
  'budgets-edit': () => (
    <Scene section="usage" usage={budgetsPage(B1, B2, B3, B4_OK, { editing: B2.id })} />
  ),
  'budgets-empty': () => <Scene section="usage" usage={budgetsPage()} />,
  rules: () => <Scene section="rules" rules={rulesFixture()} />,
  'rules-revision': () => <Scene section="rules" rules={rulesFixture({ machines: true })} />,
  'rules-builtin': () => <Scene section="rules" rules={rulesFixture({ builtinOpen: true })} />,
  'rules-builtin-confirm': () => (
    <Scene
      section="rules"
      rules={rulesFixture({ builtinOpen: true, confirm: 'default.force-push' })}
    />
  ),
  'rules-edit': () => (
    <Scene
      section="rules"
      rules={rulesFixture({
        form: {
          id: 'r2',
          command: 'curl',
          pipedTo: ['sh', 'bash'],
          action: 'ask',
          note: 'Never pipe a download into a shell unseen',
        },
      })}
    />
  ),
  'rules-edit-invalid': () => (
    <Scene
      section="rules"
      rules={rulesFixture({
        form: { kind: 'read', paths: ['/srv/data/**', 'secrets/*.pem'], action: 'deny' },
      })}
    />
  ),
  'rules-empty': () => <Scene section="rules" rules={rulesFixture({ own: [], local: false })} />,
  hooks: () => <Scene section="hooks" hooks={hooksStates.list()} />,
  'hooks-deliveries': () => <Scene section="hooks" hooks={hooksStates.deliveries()} />,
  'hooks-needs-secret': () => <Scene section="hooks" hooks={hooksStates.needsSecret()} />,
  'hooks-add': () => <Scene section="hooks" hooks={hooksStates.add()} />,
  'hooks-edit': () => <Scene section="hooks" hooks={hooksStates.edit()} />,
  'hooks-secret': () => <Scene section="hooks" hooks={hooksStates.secret()} />,
  'hooks-rotate-confirm': () => <Scene section="hooks" hooks={hooksStates.rotateConfirm()} />,
  'hooks-rotate-secret': () => <Scene section="hooks" hooks={hooksStates.rotateSecret()} />,
  'hooks-test': () => <Scene section="hooks" hooks={hooksStates.test()} />,
  'hooks-test-bad': () => <Scene section="hooks" hooks={hooksStates.testBad()} />,
  'hooks-script-run': () => <Scene section="hooks" hooks={hooksStates.scriptRun()} />,
  'hooks-empty': () => <Scene section="hooks" hooks={hooksStates.empty()} />,
  audit: () => <Scene section="audit" audit={auditFx.auditPage()} />,
  'audit-filtered': () => <Scene section="audit" audit={auditFx.auditFiltered()} />,
  'audit-detail': () => <Scene section="audit" audit={auditFx.auditDetail()} />,
  'audit-verify-running': () => <Scene section="audit" audit={auditFx.auditVerifyRunning()} />,
  'audit-verify-ok': () => <Scene section="audit" audit={auditFx.auditVerifyOk()} />,
  'audit-verify-broken': () => <Scene section="audit" audit={auditFx.auditVerifyBroken()} />,
  'audit-verify-error': () => <Scene section="audit" audit={auditFx.auditVerifyError()} />,
  'audit-export': () => <Scene section="audit" audit={auditFx.auditExport()} />,
  'audit-exported': () => <Scene section="audit" audit={auditFx.auditExported()} />,
  'audit-no-match': () => <Scene section="audit" audit={auditFx.auditNoMatch()} />,
  'audit-empty': () => <Scene section="audit" audit={auditFx.auditEmpty()} />,
  'audit-loading': () => <Scene section="audit" audit={auditFx.auditLoading()} />,
  'audit-error': () => <Scene section="audit" audit={auditFx.auditError()} />,
  templates: () => <Scene section="templates" templates={templatesFixture()} />,
  'templates-edit': () => (
    <Scene section="templates" templates={templatesFixture({ editing: 'tpl-review' })} />
  ),
  'templates-empty': () => (
    <Scene section="templates" templates={templatesFixture({ list: templateList() })} />
  ),
  'templates-import': () => (
    <Scene
      section="templates"
      templates={templatesFixture({
        devices: [STUDIO],
        list: templateList(FIX, DEPS),
        importing: { file: IMPORT_FILE, fileName: 'hoplet-templates-2026-10-21.json' },
      })}
    />
  ),
  tokens: () => <Scene section="tokens" tokens={tokensFixture()} />,
  'tokens-detail': () => <Scene section="tokens" tokens={tokensFixture({ open: 'tok_release' })} />,
  'tokens-empty': () => <Scene section="tokens" tokens={tokensFixture({ empty: true })} />,
  'tokens-issue': () => (
    <Scene section="tokens" tokens={tokensFixture({ form: filledForm({ scope: 'all' }) })} />
  ),
  'tokens-issue-answer': () => (
    <Scene
      section="tokens"
      tokens={tokensFixture({ form: filledForm({ answer: true, scope: 'all' }) })}
    />
  ),
  'tokens-picker': () => (
    <Scene
      section="tokens"
      tokens={tokensFixture({
        form: filledForm({
          allow: [{ deviceId: 'dev-build', path: '/srv/hoplet' }, { deviceId: 'dev-mbp' }],
        }),
        picker: true,
      })}
    />
  ),
  'tokens-invalid': () => (
    <Scene
      section="tokens"
      tokens={tokensFixture({
        form: filledForm({ name: '', scope: 'only', allow: [] }),
        tried: true,
      })}
    />
  ),
  'tokens-reveal': () => (
    <Scene section="tokens" tokens={tokensFixture({ issued: issuedFixture })} />
  ),
  'tokens-reveal-copied': () => (
    <Scene section="tokens" tokens={tokensFixture({ issued: issuedFixture, copied: true })} />
  ),
  'tokens-revoke': () => (
    <Scene section="tokens" tokens={tokensFixture({ confirm: 'tok_phone' })} />
  ),
  'tokens-error': () => (
    <Scene
      section="tokens"
      tokens={tokensFixture({
        form: filledForm(),
        problem:
          'The most Hoplet allows, 100 tokens, are active. Revoke one before issuing another.',
      })}
    />
  ),
  'tokens-loading': () => <Scene section="tokens" tokens={tokensFixture({ loading: true })} />,
  'tokens-load-error': () => (
    <Scene section="tokens" tokens={tokensFixture({ error: 'Could not reach the core.' })} />
  ),
};
