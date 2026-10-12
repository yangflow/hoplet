import { t } from '../../lib/i18n.ts';

/** The age of a moment as the sidebar shows it: now, 5m, 3h, 4d. */
export function compactRel(ms: number, now = Date.now()): string {
  const m = Math.floor((now - ms) / 60_000);
  if (m < 1) return t('now');
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}
