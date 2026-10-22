import * as v from 'valibot';
import { queryInt } from './common.ts';

/** Who did it (ADR 0030). Code produces `operator`, `token`, `session`, `device` and `core` today; `schedule` waits for its feature. */
export const AUDIT_ACTOR_KINDS = [
  'operator',
  'token',
  'device',
  'session',
  'schedule',
  'core',
] as const;
export const AuditActorKindSchema = v.picklist(AUDIT_ACTOR_KINDS);
export type AuditActorKind = v.InferOutput<typeof AuditActorKindSchema>;

/** How the action reached the core. */
export const AUDIT_CHANNELS = ['rest', 'link', 'agent', 'core'] as const;
export const AuditChannelSchema = v.picklist(AUDIT_CHANNELS);
export type AuditChannel = v.InferOutput<typeof AuditChannelSchema>;

/** Every type of record the log can hold. */
export const AUDIT_EVENT_TYPES = [
  'tool.call',
  'permission.requested',
  'permission.answered',
  'permission.auto',
  'mode.changed',
  'session.created',
  'session.stopped',
  'session.interrupted',
  'session.deleted',
  'session.handoff',
  'prompt.sent',
  'session.op',
  'config.changed',
  'verify.result',
  'device.token.issued',
  'token.issued',
  'token.revoked',
  'token.denied',
  'terminal.opened',
  'terminal.closed',
  'audit.started',
  'audit.checkpoint',
  'audit.exported',
] as const;
export const AuditEventTypeSchema = v.picklist(AUDIT_EVENT_TYPES);
export type AuditEventType = v.InferOutput<typeof AuditEventTypeSchema>;

/** The most bytes the canonical JSON of one record's `summary` holds. */
export const AUDIT_SUMMARY_MAX_BYTES = 2048;

/** The most records one `GET /api/audit/events` page holds. */
export const MAX_AUDIT_PAGE = 500;

const Hash = v.pipe(v.string(), v.regex(/^[0-9a-f]{64}$/));

/**
 * One record of the audit log. `summary` is the object the hash covers, as canonical JSON of at most 2 KiB;
 * `prevHash` and `hash` are the chain (SHA-256, hex).
 */
export const AuditEventSchema = v.object({
  seq: v.pipe(v.number(), v.integer(), v.minValue(1)),
  at: v.pipe(v.number(), v.integer()),
  recordedAt: v.pipe(v.number(), v.integer()),
  type: v.string(),
  sessionId: v.nullable(v.string()),
  deviceId: v.nullable(v.string()),
  actorKind: AuditActorKindSchema,
  actorId: v.string(),
  channel: AuditChannelSchema,
  ref: v.nullable(v.string()),
  summary: v.record(v.string(), v.unknown()),
  prevHash: Hash,
  hash: Hash,
});
export type AuditEvent = v.InferOutput<typeof AuditEventSchema>;

const Text = v.optional(v.pipe(v.string(), v.minLength(1), v.maxLength(1024)));
const Millis = v.optional(
  v.pipe(
    v.string(),
    v.regex(/^\d{1,15}$/),
    v.transform((raw) => Number(raw)),
  ),
);

/** The filters `GET /api/audit/events` and `GET /api/audit/export` share. */
const AuditFilterEntries = {
  session: Text,
  device: Text,
  /** An actor kind, or `kind:id`. */
  actor: v.optional(
    v.pipe(
      v.string(),
      v.regex(/^(operator|token|device|session|schedule|core)(:.{1,256})?$/),
      v.transform((raw) => {
        const colon = raw.indexOf(':');
        return colon < 0
          ? { kind: raw as AuditActorKind }
          : { kind: raw.slice(0, colon) as AuditActorKind, id: raw.slice(colon + 1) };
      }),
    ),
  ),
  /** A type, or a prefix that ends in a dot such as `permission.`. */
  type: v.optional(v.pipe(v.string(), v.regex(/^[a-z][a-z.]{0,63}$/))),
  /** Unix ms, `from` included and `to` excluded. */
  from: Millis,
  to: Millis,
  /** A plain substring of the summary. */
  q: Text,
};

/** GET /api/audit/events: newest first unless `order=asc`; `after` is the `seq` the previous page ended at. */
export const AuditEventsQuerySchema = v.object({
  ...AuditFilterEntries,
  after: Millis,
  order: v.optional(v.picklist(['asc', 'desc']), 'desc'),
  limit: queryInt(100, 1, MAX_AUDIT_PAGE),
});
export type AuditEventsQuery = v.InferOutput<typeof AuditEventsQuerySchema>;

/** GET /api/audit/export: the filters of the list and the format; the records come in `seq` order. */
export const AuditExportQuerySchema = v.object({
  ...AuditFilterEntries,
  format: v.optional(v.picklist(['jsonl', 'csv']), 'jsonl'),
});
export type AuditExportQuery = v.InferOutput<typeof AuditExportQuerySchema>;

/** The filters alone, as the store takes them. */
export type AuditFilter = Omit<AuditEventsQuery, 'after' | 'order' | 'limit'>;

/** `next` is the cursor for the following page, null at the end. */
export const AuditEventsResponseSchema = v.object({
  events: v.array(AuditEventSchema),
  next: v.nullable(v.number()),
});
export type AuditEventsResponse = v.InferOutput<typeof AuditEventsResponseSchema>;

/** GET /api/audit/verify: `head` is `<seq>:<hash>`, a head the operator wrote down elsewhere. */
export const AuditVerifyQuerySchema = v.object({
  head: v.optional(v.pipe(v.string(), v.regex(/^\d{1,15}:[0-9a-f]{64}$/))),
});
export type AuditVerifyQuery = v.InferOutput<typeof AuditVerifyQuerySchema>;

export const AUDIT_BREAK_REASONS = [
  'hash-mismatch',
  'prev-hash-mismatch',
  'gap',
  'removed-outside-retention',
  'head-mismatch',
  'head-missing',
] as const;
export const AuditBreakReasonSchema = v.picklist(AUDIT_BREAK_REASONS);
export type AuditBreakReason = v.InferOutput<typeof AuditBreakReasonSchema>;

export const AuditHeadSchema = v.object({
  seq: v.number(),
  hash: Hash,
});
export type AuditHead = v.InferOutput<typeof AuditHeadSchema>;

/**
 * The result of a walk over the chain. `firstBreak` is the first record that does not fit, with the reason;
 * `head` is the newest record (null for an empty log) and `checked` the records the walk read.
 */
export const AuditVerifyResponseSchema = v.object({
  ok: v.boolean(),
  checked: v.number(),
  head: v.nullable(AuditHeadSchema),
  firstBreak: v.nullable(
    v.object({
      seq: v.number(),
      reason: AuditBreakReasonSchema,
      message: v.string(),
    }),
  ),
});
export type AuditVerifyResponse = v.InferOutput<typeof AuditVerifyResponseSchema>;
