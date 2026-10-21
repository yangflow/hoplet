import { createHash, timingSafeEqual } from 'node:crypto';
import { log, registerSecret } from '../log/index.ts';

export interface Config {
  port: number;
  dataDir: string;
  token: string;
  webDir: string | null;
  /** The address the core is reached at from outside, for action links; null when none is set. */
  publicUrl?: string | null;
  /** Days the audit log keeps a record (`HOPLET_AUDIT_RETENTION_DAYS`); 0 keeps everything. Default 180. */
  auditRetentionDays?: number;
}

export function loadConfig(): Config {
  const port = Number(process.env.HOPLET_PORT ?? 7788);
  const dataDir = process.env.HOPLET_DATA ?? './data';
  const token = process.env.HOPLET_TOKEN ?? '';
  const webDir = process.env.HOPLET_WEB_DIR || null;
  const publicUrl = parsePublicUrl(process.env.HOPLET_PUBLIC_URL);
  if (!token) {
    log('error', 'config', 'HOPLET_TOKEN is not set; refusing to start');
    process.exit(1);
  }
  registerSecret(token);
  const auditRetentionDays = parseAuditRetention(process.env.HOPLET_AUDIT_RETENTION_DAYS);
  return { port, dataDir, token, webDir, publicUrl, auditRetentionDays };
}

/** Days of audit retention: an integer of at least 1, or 0 for no limit; 180 when absent, and for anything else (with a warning). */
export function parseAuditRetention(value: string | undefined): number {
  if (value === undefined || value.trim() === '') return 180;
  if (/^\d{1,6}$/.test(value.trim())) return Number(value.trim());
  log('warn', 'config', 'HOPLET_AUDIT_RETENTION_DAYS is not a whole number of days; using 180');
  return 180;
}

/** An http(s) origin and path without a trailing slash, or null (and a warning) for anything else. */
export function parsePublicUrl(value: string | undefined): string | null {
  if (!value) return null;
  try {
    const url = new URL(value);
    if (url.protocol !== 'https:' && url.protocol !== 'http:') throw new Error('scheme');
    if (url.username || url.password || url.search || url.hash) throw new Error('parts');
    return `${url.origin}${url.pathname.replace(/\/+$/, '')}`;
  } catch {
    log('warn', 'config', 'HOPLET_PUBLIC_URL is not an http(s) URL; action links are off');
    return null;
  }
}

const digest = (value: string): Buffer => createHash('sha256').update(value).digest();

/**
 * Whether `presented` equals `secret`, in time that does not depend on how much of it matches or on its
 * length: both are compared as fixed-size digests.
 */
export function secretsMatch(presented: string, secret: string): boolean {
  return timingSafeEqual(digest(presented), digest(secret)) && secret.length > 0;
}

/** The token a request presents as `Authorization: Bearer <token>`, or '' when there is none. */
export function bearerToken(authorization: string | undefined): string {
  return typeof authorization === 'string' && authorization.startsWith('Bearer ')
    ? authorization.slice('Bearer '.length)
    : '';
}
