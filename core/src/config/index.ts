import { createHash, timingSafeEqual } from 'node:crypto';
import { log, registerSecret } from '../log/index.ts';

export interface Config {
  port: number;
  dataDir: string;
  token: string;
  webDir: string | null;
}

export function loadConfig(): Config {
  const port = Number(process.env.HOPLET_PORT ?? 7788);
  const dataDir = process.env.HOPLET_DATA ?? './data';
  const token = process.env.HOPLET_TOKEN ?? '';
  const webDir = process.env.HOPLET_WEB_DIR || null;
  if (!token) {
    log('error', 'config', 'HOPLET_TOKEN is not set; refusing to start');
    process.exit(1);
  }
  registerSecret(token);
  return { port, dataDir, token, webDir };
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
