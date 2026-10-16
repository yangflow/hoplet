// Action tokens (ADR 0021): a request and an expiry signed with HMAC-SHA-256 by a key of the core.

import { createHmac, randomBytes, timingSafeEqual } from 'node:crypto';

/** The longest a link works. */
export const LINK_MAX_MS = 10 * 60_000;

export interface ActionClaims {
  /** The token's own ID, what single use is tracked by. */
  id: string;
  sessionId: string;
  requestId: string;
  /** Epoch milliseconds. */
  expiresAt: number;
}

const mac = (key: string, payload: string): Buffer =>
  createHmac('sha256', Buffer.from(key, 'base64url')).update(payload).digest();

export function signToken(key: string, claims: Omit<ActionClaims, 'id'>): string {
  const full: ActionClaims = { id: randomBytes(9).toString('base64url'), ...claims };
  const payload = Buffer.from(
    JSON.stringify({ i: full.id, s: full.sessionId, r: full.requestId, e: full.expiresAt }),
  ).toString('base64url');
  return `${payload}.${mac(key, payload).toString('base64url')}`;
}

/** The claims of a token this key signed, or null for any other string. Expiry is the caller's check. */
export function verifyToken(key: string, token: string): ActionClaims | null {
  const [payload, signature, extra] = token.split('.');
  if (!payload || !signature || extra !== undefined) return null;
  const given = Buffer.from(signature, 'base64url');
  const expected = mac(key, payload);
  if (given.length !== expected.length || !timingSafeEqual(given, expected)) return null;
  try {
    const raw = JSON.parse(Buffer.from(payload, 'base64url').toString('utf8')) as Record<
      string,
      unknown
    >;
    if (
      typeof raw.i !== 'string' ||
      typeof raw.s !== 'string' ||
      typeof raw.r !== 'string' ||
      typeof raw.e !== 'number'
    )
      return null;
    return { id: raw.i, sessionId: raw.s, requestId: raw.r, expiresAt: raw.e };
  } catch {
    return null;
  }
}
