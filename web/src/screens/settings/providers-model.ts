import type { DeviceInfo, LiveSessionMeta, ProviderInfo } from '@hoplet/shared';

/** What the sessions say about one provider on one machine. */
export type ProviderUse = { sessions: number; lastUsed: number | undefined };

type Session = Pick<LiveSessionMeta, 'provider' | 'ownerDeviceId' | 'updatedAt'>;

/**
 * How many sessions a machine has with a provider and when the newest was last updated, from the session list the
 * web already holds. Archived sessions count: the row tells how much the provider is used on the machine, not what
 * the sidebar lists. A session belongs to the machine that owns it; one without an owner belongs to none.
 */
export function providerUse(
  sessions: readonly Session[],
  device: Pick<DeviceInfo, 'deviceId'>,
  provider: ProviderInfo['provider'],
): ProviderUse {
  let count = 0;
  let last: number | undefined;
  for (const s of sessions) {
    if (s.provider !== provider) continue;
    if (s.ownerDeviceId !== device.deviceId) continue;
    count++;
    if (last === undefined || s.updatedAt > last) last = s.updatedAt;
  }
  return { sessions: count, lastUsed: last };
}

/** The badge of a provider's version: `tested`, `newer` than the tested ones, or none when the version is unknown. */
export function versionBadge(
  p: Pick<ProviderInfo, 'version' | 'tested'>,
): 'tested' | 'newer' | null {
  if (p.version === undefined || p.tested === undefined) return null;
  return p.tested ? 'tested' : 'newer';
}

/** The machine a section opens on: the first connected one, else the first. */
export function defaultMachine(
  devices: readonly Pick<DeviceInfo, 'deviceId' | 'connected'>[],
): string | undefined {
  return (devices.find((d) => d.connected) ?? devices[0])?.deviceId;
}
