// API tokens (#71, ADR 0033): the table `api_tokens`, shared by the scoped tokens the host mints and the session
// tokens of #67. A secret is returned once, at creation; the table keeps its SHA-256 only.
import { createHash, randomBytes } from 'node:crypto';
import {
  MAX_ACTIVE_TOKENS,
  safeParse,
  type TokenGrant,
  TokenGrantSchema,
  type TokenInfo,
  type TokenKind,
} from '@hoplet/shared';
import { registerSecret } from '../log/index.ts';
import { appendAudit } from './audit.ts';
import type { StoreContext } from './layout.ts';

/** The shape of every secret: the prefix lets the scrubber and a secret scanner recognize it. */
export const TOKEN_PREFIX = 'hpt_';

/** `last_used_at` is written at most this often per token. */
const LAST_USED_RESOLUTION_MS = 60_000;

/** More tokens are active than the limit allows. */
export class TooManyTokensError extends Error {
  constructor() {
    super(`at most ${MAX_ACTIVE_TOKENS} tokens can be active`);
  }
}

interface Row {
  id: string;
  kind: string;
  name: string;
  grant: string;
  created_at: number;
  expires_at: number;
  revoked_at: number | null;
  last_used_at: number | null;
}

/** The token a presented secret names: what the router needs to decide a request. */
export interface TokenRecord {
  id: string;
  kind: TokenKind;
  name: string;
  grant: TokenGrant;
  expiresAt: number;
}

export function hashSecret(secret: string): string {
  return createHash('sha256').update(secret).digest('hex');
}

function infoOf(row: Row, now: number): TokenInfo {
  const parsed = safeParse(TokenGrantSchema, JSON.parse(row.grant));
  // A row the core wrote parses; a damaged one reaches nothing.
  const grant: TokenGrant = parsed.success ? parsed.output : { capabilities: [], allow: [] };
  const revokedAt = row.revoked_at === null ? null : Number(row.revoked_at);
  const expiresAt = Number(row.expires_at);
  return {
    id: row.id,
    kind: row.kind === 'session' ? 'session' : 'scoped',
    name: row.name,
    grant,
    createdAt: Number(row.created_at),
    expiresAt,
    revokedAt,
    lastUsedAt: row.last_used_at === null ? null : Number(row.last_used_at),
    state: revokedAt !== null ? 'revoked' : expiresAt <= now ? 'expired' : 'active',
  };
}

const COLUMNS = 'id, kind, name, grant, created_at, expires_at, revoked_at, last_used_at';

export function listTokens(ctx: StoreContext, now: number): TokenInfo[] {
  const rows = ctx.db
    .prepare(`SELECT ${COLUMNS} FROM api_tokens ORDER BY created_at DESC, id`)
    .all() as unknown as Row[];
  return rows.map((row) => infoOf(row, now));
}

/**
 * Creates a token and records `token.issued` with it. Returns its record and the secret, which nothing can
 * return again.
 */
export function issueToken(
  ctx: StoreContext,
  input: {
    name: string;
    grant: TokenGrant;
    expiresAt: number;
    kind?: TokenKind;
    actor?: { kind: 'operator' | 'core'; id?: string };
  },
  now: number,
): { info: TokenInfo; secret: string } {
  const secret = `${TOKEN_PREFIX}${randomBytes(32).toString('base64url')}`;
  registerSecret(secret);
  const id = randomBytes(8).toString('hex');
  const own = !ctx.db.isTransaction;
  if (own) ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const active = ctx.db
      .prepare(`SELECT COUNT(*) AS n FROM api_tokens WHERE revoked_at IS NULL AND expires_at > ?`)
      .get(now) as { n: number };
    if (Number(active.n) >= MAX_ACTIVE_TOKENS) throw new TooManyTokensError();
    ctx.db
      .prepare(
        `INSERT INTO api_tokens(id, kind, name, token_hash, grant, created_at, expires_at)
         VALUES(?, ?, ?, ?, ?, ?, ?)`,
      )
      .run(
        id,
        input.kind ?? 'scoped',
        input.name,
        hashSecret(secret),
        JSON.stringify(input.grant),
        now,
        input.expiresAt,
      );
    appendAudit(ctx, {
      type: 'token.issued',
      actor: input.actor ?? { kind: 'operator' },
      summary: { id, name: input.name, grant: input.grant, expiresAt: input.expiresAt },
    });
    if (own) ctx.db.exec('COMMIT');
  } catch (err) {
    if (own && ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
  const row = ctx.db
    .prepare(`SELECT ${COLUMNS} FROM api_tokens WHERE id = ?`)
    .get(id) as unknown as Row;
  return { info: infoOf(row, now), secret };
}

/**
 * Ends a token. An active token is revoked at once (its row stays, so lists and the audit log make sense); a
 * revoked or expired one is removed. Returns what happened, or `unknown` for an id that is not a token.
 */
export function revokeToken(
  ctx: StoreContext,
  id: string,
  now: number,
): 'revoked' | 'removed' | 'unknown' {
  const own = !ctx.db.isTransaction;
  if (own) ctx.db.exec('BEGIN IMMEDIATE');
  try {
    const row = ctx.db.prepare(`SELECT ${COLUMNS} FROM api_tokens WHERE id = ?`).get(id) as
      | Row
      | undefined;
    let outcome: 'revoked' | 'removed' | 'unknown' = 'unknown';
    if (row) {
      if (infoOf(row, now).state === 'active') {
        ctx.db.prepare(`UPDATE api_tokens SET revoked_at = ? WHERE id = ?`).run(now, id);
        appendAudit(ctx, { type: 'token.revoked', summary: { id, name: row.name } });
        outcome = 'revoked';
      } else {
        ctx.db.prepare(`DELETE FROM api_tokens WHERE id = ?`).run(id);
        outcome = 'removed';
      }
    }
    if (own) ctx.db.exec('COMMIT');
    return outcome;
  } catch (err) {
    if (own && ctx.db.isTransaction) ctx.db.exec('ROLLBACK');
    throw err;
  }
}

/**
 * The token a presented secret names, or null for one that is unknown, expired or revoked (the three are not told
 * apart). One indexed read per request and no cache, so a revoke takes effect on the next request.
 */
export function tokenForSecret(ctx: StoreContext, secret: string, now: number): TokenRecord | null {
  if (!secret.startsWith(TOKEN_PREFIX)) return null;
  const row = ctx.db
    .prepare(`SELECT ${COLUMNS} FROM api_tokens WHERE token_hash = ?`)
    .get(hashSecret(secret)) as Row | undefined;
  if (!row) return null;
  const info = infoOf(row, now);
  if (info.state !== 'active') return null;
  if (info.lastUsedAt === null || now - info.lastUsedAt >= LAST_USED_RESOLUTION_MS) {
    ctx.db.prepare(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`).run(now, info.id);
  }
  return {
    id: info.id,
    kind: info.kind,
    name: info.name,
    grant: info.grant,
    expiresAt: info.expiresAt,
  };
}

/** Whether the token is still active: the browser WebSocket asks it to close a connection that outlived it. */
export function tokenActive(ctx: StoreContext, id: string, now: number): boolean {
  const row = ctx.db
    .prepare(`SELECT revoked_at, expires_at FROM api_tokens WHERE id = ?`)
    .get(id) as { revoked_at: number | null; expires_at: number } | undefined;
  return !!row && row.revoked_at === null && Number(row.expires_at) > now;
}
