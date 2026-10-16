// The action page's pure parts (ADR 0021): reading the link token from the URL fragment and saying how long it lasts.
import { t } from '../../lib/i18n.ts';

const TOKEN = /^[A-Za-z0-9_-]{8,1024}\.[A-Za-z0-9_-]{8,128}$/;

/** The token in a URL fragment (`#<token>`), or null when the fragment holds none. */
export function tokenFromHash(hash: string): string | null {
  const raw = hash.startsWith('#') ? hash.slice(1) : hash;
  return TOKEN.test(raw) ? raw : null;
}

/** "Link valid for 8 more minutes": the time left, rounded up to whole minutes; null once it has passed. */
export function expiryText(expiresAt: number | undefined, now: number): string | null {
  if (expiresAt === undefined) return null;
  const left = expiresAt - now;
  if (left <= 0) return null;
  const minutes = Math.max(1, Math.ceil(left / 60_000));
  return minutes === 1
    ? t('Link valid for 1 more minute')
    : t('Link valid for {n} more minutes').replace('{n}', String(minutes));
}
