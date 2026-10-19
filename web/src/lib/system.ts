import type { DeviceSystem } from '@hoplet/shared';
import { t } from './i18n.ts';

export type SystemKind = 'macos' | 'windows' | 'linux' | 'other';

/** The mark family of an agent's `GOOS`; anything else, and an agent that reported none, is `other`. */
export function systemOf(os: string | null | undefined): SystemKind {
  switch (os) {
    case 'darwin':
      return 'macos';
    case 'windows':
      return 'windows';
    case 'linux':
      return 'linux';
    default:
      return 'other';
  }
}

/** The name the interface shows for a system, or null when it has no name worth showing. */
export function systemName(os: string | null | undefined): string | null {
  switch (systemOf(os)) {
    case 'macos':
      return 'macOS';
    case 'windows':
      return 'Windows';
    case 'linux':
      return 'Linux';
    default:
      return null;
  }
}

/** The accessible name of a machine: "<name>, <system>, online|offline"; the system is left out when unknown. */
export function machineLabel(
  name: string,
  system: DeviceSystem | null | undefined,
  online: boolean,
): string {
  const os = systemName(system?.os);
  const presence = online ? t('online') : t('offline');
  return os ? `${name}, ${os}, ${presence}` : `${name}, ${presence}`;
}
