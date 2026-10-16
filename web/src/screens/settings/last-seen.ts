import { t } from '../../lib/i18n.ts';

/** When a machine last connected, for the Machines list: "now" while it is connected, then "5 min ago", "6 h ago". */
export function lastSeenText(
  device: { connected: boolean; lastSeen?: number | undefined },
  now = Date.now(),
): string {
  if (device.connected) return t('now');
  if (device.lastSeen === undefined) return '—';
  const minutes = Math.max(0, Math.floor((now - device.lastSeen) / 60_000));
  if (minutes < 1) return t('now');
  if (minutes < 60) return `${minutes} ${t('min ago')}`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} ${t('h ago')}`;
  return `${Math.floor(hours / 24)} ${t('d ago')}`;
}
