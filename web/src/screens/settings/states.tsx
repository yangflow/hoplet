import type { DeviceInfo, NotifyPreferences, WebhookInfo } from '@hoplet/shared';
import { type ReactNode, useEffect } from 'react';
import { type SectionId, Settings } from './index.tsx';
import type { MachinesFixture } from './machines.tsx';
import type { NotifyFixture, PushState } from './notifications.tsx';
import { type Appearance, applyText, DEFAULTS } from './prefs.ts';

// Fixtures for /__states/settings/<state>: every state of design/screens/settings.html.
// Nothing here is read by the real screen.

const NOW = Date.UTC(2026, 9, 8, 12, 0, 0);

function machine(deviceId: string, name: string, connected: boolean, lastSeen: number): DeviceInfo {
  return { deviceId, name, connected, lastSeen, providers: [] };
}

const DEVICES: DeviceInfo[] = [
  machine('dev-macbook', 'MacBook Pro', true, NOW),
  machine('dev-studio', 'studio', true, NOW),
  machine('dev-office', 'office-mini', false, NOW - 6 * 3_600_000),
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

/** The page with its text choices in effect, the way the app applies them; the theme is left to the URL. */
function Scene(props: {
  section: SectionId;
  appearance?: Partial<Appearance>;
  machines?: MachinesFixture;
  notify?: NotifyFixture;
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
