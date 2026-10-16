import type { DeviceInfo } from '@hoplet/shared';
import { type ReactNode, useEffect } from 'react';
import { type SectionId, Settings } from './index.tsx';
import type { MachinesFixture } from './machines.tsx';
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

/** The page with its text choices in effect, the way the app applies them; the theme is left to the URL. */
function Scene(props: {
  section: SectionId;
  appearance?: Partial<Appearance>;
  machines?: MachinesFixture;
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
};
