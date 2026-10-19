import type {
  ConfigOption,
  DeviceInfo,
  LiveSessionMeta,
  NotifyPreferences,
  ProviderInfo,
  WebhookInfo,
} from '@hoplet/shared';
import { type ReactNode, useEffect } from 'react';
import { type Appearance, applyText, DEFAULTS } from '../../lib/prefs.ts';
import { type SectionId, Settings } from './index.tsx';
import type { MachinesFixture } from './machines.tsx';
import type { NotifyFixture, PushState } from './notifications.tsx';
import type { ProvidersFixture } from './providers.tsx';

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
  };
}

const DEVICES: DeviceInfo[] = [
  machine('dev-macbook', 'MacBook Pro', true, NOW, 'darwin'),
  machine('dev-studio', 'studio', true, NOW, 'linux'),
  machine('dev-office', 'office-mini', false, NOW - 6 * 3_600_000, 'windows'),
];

const STUDIO_2 = { deviceId: 'dev-studio-2', name: 'studio-2', token: 'fixture-token' };

function machines(mint?: { copied: boolean }, online = false): MachinesFixture {
  return {
    now: NOW,
    devices: online ? [...DEVICES, machine(STUDIO_2.deviceId, STUDIO_2.name, true, NOW)] : DEVICES,
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

function announced(
  provider: ProviderInfo['provider'],
  version: string,
  tested: boolean,
): ProviderInfo {
  return {
    provider,
    version,
    tested,
    capabilities: {
      permissions: true,
      interrupt: true,
      images: true,
      steer: true,
      config: provider === 'claude' ? CLAUDE_CONFIG : CODEX_CONFIG,
    },
  };
}

function online(deviceId: string, name: string, providers: ProviderInfo[]): DeviceInfo {
  return { ...machine(deviceId, name, true, NOW), providers };
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
  over: { offline?: boolean; none?: boolean; empty?: boolean } = {},
): ProvidersFixture {
  const devices = over.empty
    ? []
    : [
        online('dev-macbook', 'MacBook Pro', [
          announced('claude', '2.1.42', true),
          announced('codex', '0.124.0', true),
        ]),
        online('dev-studio', 'studio', [
          announced('claude', '2.2.0', false),
          announced('codex', '0.124.0', true),
        ]),
        ...(over.offline ? [machine('dev-office', 'office-mini', false, NOW - 3 * HOUR)] : []),
        ...(over.none ? [online('dev-nas', 'nas-box', [])] : []),
      ];
  return {
    devices,
    sessions: PROVIDER_SESSIONS,
    now: NOW,
    ...(selected ? { selected } : {}),
    ...(selected === undefined && !over.empty ? { open: ['dev-macbook/claude'] } : {}),
  };
}

/** The page with its text choices in effect, the way the app applies them; the theme is left to the URL. */
function Scene(props: {
  section: SectionId;
  appearance?: Partial<Appearance>;
  machines?: MachinesFixture;
  notify?: NotifyFixture;
  providers?: ProvidersFixture;
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
  'settings-providers': () => <Scene section="providers" providers={providers(undefined)} />,
  'settings-providers-newer': () => (
    <Scene section="providers" providers={providers('dev-studio')} />
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
};
