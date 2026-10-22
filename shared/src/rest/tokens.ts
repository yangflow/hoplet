import * as v from 'valibot';
import { TrimmedSchema } from './common.ts';

/**
 * What a scoped API token may do (ADR 0033). Each is a statement about routes: `read` sees sessions, machines and
 * the live stream; `send` adds to an existing session; `create` starts sessions; `answer` answers permission
 * requests. Everything else (deleting, configuration, terminals, handoff, administration) is host-only.
 * `request-handoff` asks the user to hand the session off (ADR 0034): only a session token can hold it, and
 * POST /api/tokens answers `invalid-request` to a grant that names it.
 */
export const TOKEN_CAPABILITIES = ['read', 'send', 'create', 'answer', 'request-handoff'] as const;
export const TokenCapabilitySchema = v.picklist(TOKEN_CAPABILITIES);
export type TokenCapability = v.InferOutput<typeof TokenCapabilitySchema>;

/** The most entries one `allow` list holds. */
export const MAX_TOKEN_ALLOW = 50;

/** The longest life of a token, in days; and the one Settings offers first. */
export const MAX_TOKEN_DAYS = 365;
export const DEFAULT_TOKEN_DAYS = 90;

/** The most tokens that are active (not expired, not revoked) at once. */
export const MAX_ACTIVE_TOKENS = 100;

/** A machine (`deviceId` alone) or one project of it (`path`): the places a token reaches. */
export const TokenAllowEntrySchema = v.strictObject({
  deviceId: v.pipe(TrimmedSchema, v.maxLength(128)),
  path: v.optional(v.pipe(TrimmedSchema, v.maxLength(4096))),
});
export type TokenAllowEntry = v.InferOutput<typeof TokenAllowEntrySchema>;

/**
 * The permission model of a token (ADR 0033, shared with the session tokens of #67): what it may do, and where.
 * `allow` absent means every machine and project; present, it lists the only places the token reaches.
 */
export const TokenGrantSchema = v.strictObject({
  capabilities: v.pipe(
    v.array(TokenCapabilitySchema),
    v.minLength(1),
    v.maxLength(TOKEN_CAPABILITIES.length),
    v.check((list) => new Set(list).size === list.length),
  ),
  allow: v.optional(
    v.pipe(v.array(TokenAllowEntrySchema), v.minLength(1), v.maxLength(MAX_TOKEN_ALLOW)),
  ),
});
export type TokenGrant = v.InferOutput<typeof TokenGrantSchema>;

export const TokenKindSchema = v.picklist(['scoped', 'session']);
export type TokenKind = v.InferOutput<typeof TokenKindSchema>;

export const TokenStateSchema = v.picklist(['active', 'expired', 'revoked']);
export type TokenState = v.InferOutput<typeof TokenStateSchema>;

/** A token as lists show it: never the secret and never its hash. */
export const TokenInfoSchema = v.object({
  id: v.string(),
  kind: TokenKindSchema,
  name: v.string(),
  grant: TokenGrantSchema,
  createdAt: v.number(),
  expiresAt: v.number(),
  revokedAt: v.nullable(v.number()),
  /** Unix ms of the last request, at a precision of one minute; null while it was never used. */
  lastUsedAt: v.nullable(v.number()),
  state: TokenStateSchema,
});
export type TokenInfo = v.InferOutput<typeof TokenInfoSchema>;

export const TokenListResponseSchema = v.object({ tokens: v.array(TokenInfoSchema) });
export type TokenListResponse = v.InferOutput<typeof TokenListResponseSchema>;

/** POST /api/tokens. The life is required: 1 to 365 days (Settings offers 1, 7, 30 and 90). */
export const TokenCreateRequestSchema = v.object({
  name: v.pipe(TrimmedSchema, v.maxLength(80)),
  grant: TokenGrantSchema,
  expiresInDays: v.pipe(v.number(), v.integer(), v.minValue(1), v.maxValue(MAX_TOKEN_DAYS)),
});
export type TokenCreateRequest = v.InferOutput<typeof TokenCreateRequestSchema>;

/** The answer of POST /api/tokens: the token's record and its secret, which is shown this once. */
export const TokenCreateResponseSchema = v.object({
  ...TokenInfoSchema.entries,
  token: v.string(),
});
export type TokenCreateResponse = v.InferOutput<typeof TokenCreateResponseSchema>;

/** The answer of DELETE /api/tokens/:id. */
export const TokenRevokeResponseSchema = v.object({ ok: v.literal(true) });
export type TokenRevokeResponse = v.InferOutput<typeof TokenRevokeResponseSchema>;

/** Whether `grant` lets its holder do what `capability` stands for. */
export function grantHas(grant: TokenGrant, capability: TokenCapability): boolean {
  return grant.capabilities.includes(capability);
}

/** Whether `path` is `base` or below it; the separator is the one `base` uses, and a trailing one is ignored. */
export function pathWithin(path: string, base: string): boolean {
  if (path.split(/[/\\]/).includes('..')) return false;
  const sep = base.includes('\\') && !base.includes('/') ? '\\' : '/';
  const root = base.length > 1 && base.endsWith(sep) ? base.slice(0, -1) : base;
  return path === root || path.startsWith(root + sep) || (root === sep && path.startsWith(sep));
}

/** Whether the grant reaches any part of the machine: it lists the machine, or a project of it. */
export function grantSeesDevice(grant: TokenGrant, deviceId: string): boolean {
  return grant.allow === undefined || grant.allow.some((e) => e.deviceId === deviceId);
}

/** Whether the grant reaches the whole machine, not only listed projects of it. */
export function grantSeesWholeDevice(grant: TokenGrant, deviceId: string): boolean {
  return (
    grant.allow === undefined ||
    grant.allow.some((e) => e.deviceId === deviceId && e.path === undefined)
  );
}

/** Whether the grant reaches the project at `path` of the machine (or a directory below it). */
export function grantSeesProject(grant: TokenGrant, deviceId: string, path: string): boolean {
  return (
    grant.allow === undefined ||
    grant.allow.some(
      (e) => e.deviceId === deviceId && (e.path === undefined || pathWithin(path, e.path)),
    )
  );
}
