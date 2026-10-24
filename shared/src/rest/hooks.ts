import * as v from 'valibot';
import { HandoffIdSchema, SessionIdSchema } from '../ids.ts';
import { TrimmedSchema } from './common.ts';

/** The lifecycle events a hook can receive (ADR 0031). */
export const HOOK_EVENT_TYPES = [
  'session.started',
  'turn.completed',
  'session.ended',
  'permission.requested',
  'permission.resolved',
  'session.error',
  'handoff.started',
  'handoff.completed',
] as const;
export const HookEventTypeSchema = v.picklist(HOOK_EVENT_TYPES);
export type HookEventType = v.InferOutput<typeof HookEventTypeSchema>;

/** `hook.test` is only sent by the test route; it is never in a hook's `events`. */
export const HOOK_TEST_EVENT = 'hook.test';

/** The version of the envelope every event is sent in. */
export const HOOK_ENVELOPE_VERSION = 1;

/** The most hooks. */
export const MAX_HOOKS = 20;

/** Who caused the event: a person, a link in a notification, a permission rule, or Hoplet itself. */
export const HookActorSchema = v.variant('kind', [
  v.object({ kind: v.literal('user') }),
  v.object({ kind: v.literal('action-link') }),
  v.object({ kind: v.literal('rule'), id: v.string() }),
  v.object({ kind: v.literal('system') }),
]);
export type HookActor = v.InferOutput<typeof HookActorSchema>;

const MachineSchema = v.object({ id: v.string(), name: v.string() });

const EnvelopeBase = {
  v: v.literal(1),
  /** `evt_` and a ULID: unique, and sorted by the time the event happened. */
  id: v.pipe(v.string(), v.regex(/^evt_[0-9A-HJKMNP-TV-Z]{26}$/)),
  /** ISO 8601, UTC: when the event happened. */
  at: v.string(),
  /** The machine was offline when the event happened and sent it after it reconnected (ADR 0039). */
  replayed: v.optional(v.literal(true)),
  actor: HookActorSchema,
};

const SessionRefSchema = v.object({
  id: SessionIdSchema,
  /** `path` is the project directory on its machine; only the copy a script hook receives carries it. */
  project: v.object({ name: v.string(), path: v.optional(v.string()) }),
  machine: MachineSchema,
});

/** A session that may not exist: a start that failed, or a test. */
const MaybeSessionRefSchema = v.object({
  id: v.optional(SessionIdSchema),
  project: v.object({ name: v.string(), path: v.optional(v.string()) }),
  machine: MachineSchema,
});

const ResolutionSchema = v.picklist(['allow', 'deny', 'expired', 'abandoned']);

export const HookEnvelopeSchema = v.variant('type', [
  v.object({
    ...EnvelopeBase,
    type: v.literal('session.started'),
    session: SessionRefSchema,
    data: v.object({
      provider: v.string(),
      configuration: v.optional(v.string()),
      origin: v.picklist(['new', 'plan']),
    }),
  }),
  v.object({
    ...EnvelopeBase,
    type: v.literal('turn.completed'),
    session: SessionRefSchema,
    data: v.object({
      turn: v.number(),
      durationMs: v.number(),
      /** With `summary`: the session's title, at most 80 characters. */
      title: v.optional(v.string()),
      /** With `summary`: the session's usage so far. */
      usage: v.optional(
        v.object({
          inputTokens: v.number(),
          outputTokens: v.number(),
          costUsd: v.optional(v.number()),
        }),
      ),
    }),
  }),
  v.object({
    ...EnvelopeBase,
    type: v.literal('session.ended'),
    session: SessionRefSchema,
    data: v.object({ reason: v.picklist(['stopped', 'idle', 'deleted']) }),
  }),
  v.object({
    ...EnvelopeBase,
    type: v.literal('permission.requested'),
    session: SessionRefSchema,
    data: v.object({
      requestId: v.string(),
      tool: v.string(),
      plan: v.boolean(),
      /** Epoch milliseconds; absent for a plan review, which never expires. */
      expiresAt: v.optional(v.number()),
      /** With `summary`: the redacted command or path, at most 80 characters. */
      summary: v.optional(v.string()),
    }),
  }),
  v.object({
    ...EnvelopeBase,
    type: v.literal('permission.resolved'),
    session: SessionRefSchema,
    data: v.object({ requestId: v.string(), resolution: ResolutionSchema }),
  }),
  v.object({
    ...EnvelopeBase,
    type: v.literal('session.error'),
    session: MaybeSessionRefSchema,
    data: v.object({
      stage: v.picklist(['start', 'runtime', 'handoff']),
      code: v.string(),
      /** With `summary`: the redacted reason, at most 200 characters. */
      reason: v.optional(v.string()),
    }),
  }),
  v.object({
    ...EnvelopeBase,
    type: v.literal('handoff.started'),
    session: SessionRefSchema,
    data: v.object({ handoffId: HandoffIdSchema, from: MachineSchema, to: MachineSchema }),
  }),
  v.object({
    ...EnvelopeBase,
    type: v.literal('handoff.completed'),
    session: SessionRefSchema,
    data: v.object({ handoffId: HandoffIdSchema, from: MachineSchema, to: MachineSchema }),
  }),
  v.object({
    ...EnvelopeBase,
    type: v.literal('hook.test'),
    session: MaybeSessionRefSchema,
    data: v.object({ hookId: v.string() }),
  }),
]);
export type HookEnvelope = v.InferOutput<typeof HookEnvelopeSchema>;

const HookUrlSchema = v.pipe(v.string(), v.url(), v.maxLength(2048), v.regex(/^https?:\/\//));

/** The events one hook receives: a non-empty set of types, each once. */
export const HookEventsSchema = v.pipe(
  v.array(HookEventTypeSchema),
  v.minLength(1),
  v.maxLength(HOOK_EVENT_TYPES.length),
  v.check((types) => new Set(types).size === types.length, 'Events must not repeat'),
);

/** Which sessions a hook hears about: all of them, or one project on one machine. */
export const HookScopeSchema = v.variant('kind', [
  v.object({ kind: v.literal('all') }),
  v.object({
    kind: v.literal('project'),
    deviceId: v.pipe(TrimmedSchema, v.maxLength(200)),
    projectPath: v.pipe(TrimmedSchema, v.maxLength(4096)),
  }),
]);
export type HookScope = v.InferOutput<typeof HookScopeSchema>;

/** POST /api/hooks */
export const HookCreateRequestSchema = v.strictObject({
  url: HookUrlSchema,
  label: v.pipe(TrimmedSchema, v.maxLength(80)),
  events: HookEventsSchema,
  scope: v.optional(HookScopeSchema),
  /** Adds the opt-in fields (a title, usage, a redacted summary) to the events. */
  summary: v.optional(v.boolean()),
  enabled: v.optional(v.boolean()),
});
export type HookCreateRequest = v.InferOutput<typeof HookCreateRequestSchema>;

/** PATCH /api/hooks/:id */
export const HookUpdateRequestSchema = v.strictObject({
  url: v.optional(HookUrlSchema),
  label: v.optional(v.pipe(TrimmedSchema, v.maxLength(80))),
  events: v.optional(HookEventsSchema),
  scope: v.optional(HookScopeSchema),
  summary: v.optional(v.boolean()),
  enabled: v.optional(v.boolean()),
});
export type HookUpdateRequest = v.InferOutput<typeof HookUpdateRequestSchema>;

/** `needs-secret`: the secret file lost this hook's secret; nothing is sent until it is rotated. */
export const HOOK_STATUSES = ['active', 'disabled', 'needs-secret'] as const;
export const HookStatusSchema = v.picklist(HOOK_STATUSES);
export type HookStatus = v.InferOutput<typeof HookStatusSchema>;

export const HOOK_DELIVERY_STATES = ['pending', 'delivered', 'failed', 'dropped'] as const;
export const HookDeliveryStateSchema = v.picklist(HOOK_DELIVERY_STATES);
export type HookDeliveryState = v.InferOutput<typeof HookDeliveryStateSchema>;

const LastDeliverySchema = v.object({
  at: v.number(),
  state: HookDeliveryStateSchema,
  error: v.optional(v.string()),
});

export const WebhookHookInfoSchema = v.object({
  id: v.string(),
  kind: v.literal('webhook'),
  label: v.string(),
  /** The URL without its query string, which may carry a credential. */
  url: v.string(),
  events: HookEventsSchema,
  scope: HookScopeSchema,
  summary: v.boolean(),
  enabled: v.boolean(),
  status: HookStatusSchema,
  /** The envelope version this hook is sent. */
  version: v.literal(1),
  createdAt: v.number(),
  updatedAt: v.number(),
  /** Deliveries waiting to be sent or retried. */
  pending: v.number(),
  lastDelivery: v.optional(LastDeliverySchema),
});
export type WebhookHookInfo = v.InferOutput<typeof WebhookHookInfoSchema>;

/**
 * A script hook (ADR 0031): declared in the agent's own `config.json`, so it is shown and can be switched or
 * tested, never created, edited or deleted here. Its `id` is `script:<deviceId>:<slug>`.
 */
export const ScriptHookInfoSchema = v.object({
  id: v.string(),
  kind: v.literal('script'),
  /** The slug of the entry in `config.json`. */
  label: v.string(),
  machine: v.object({ id: v.string(), name: v.string(), online: v.boolean() }),
  /** The absolute path of the program, as the agent announced it. */
  command: v.string(),
  events: v.pipe(v.array(v.union([HookEventTypeSchema, v.literal('*')])), v.minLength(1)),
  /** Project paths on the machine; empty means every project. */
  projects: v.array(v.string()),
  timeoutMs: v.number(),
  /** The names, never the values, of the variables the script is passed. */
  env: v.array(v.string()),
  /** What the agent counted since it started. */
  runs: v.number(),
  failed: v.number(),
  dropped: v.number(),
  enabled: v.boolean(),
  /** `disabled` is the core's switch. A script that is not announced any more is not listed. */
  status: v.picklist(['active', 'disabled']),
  updatedAt: v.number(),
  pending: v.number(),
  lastDelivery: v.optional(LastDeliverySchema),
});
export type ScriptHookInfo = v.InferOutput<typeof ScriptHookInfoSchema>;

export const HookInfoSchema = v.variant('kind', [WebhookHookInfoSchema, ScriptHookInfoSchema]);
export type HookInfo = v.InferOutput<typeof HookInfoSchema>;

export const HookListResponseSchema = v.object({ hooks: v.array(HookInfoSchema) });
export type HookListResponse = v.InferOutput<typeof HookListResponseSchema>;

/** POST /api/hooks and /api/hooks/:id/rotate-secret: the signing secret is returned this once. */
export const HookSecretResponseSchema = v.object({
  hook: WebhookHookInfoSchema,
  secret: v.string(),
});
export type HookSecretResponse = v.InferOutput<typeof HookSecretResponseSchema>;

export const HookDeliverySchema = v.object({
  id: v.string(),
  eventId: v.string(),
  type: v.string(),
  state: HookDeliveryStateSchema,
  attempts: v.number(),
  /**
   * A webhook: `http-<status>`, `timeout`, `blocked-address`, `network`, `needs-secret`, `overflow` or `expired`.
   * A script: `exit-<code>`, `timeout`, `spawn-failed`, `offline`, `disabled`, `connection-lost`, `interrupted` or `dropped`.
   */
  error: v.optional(v.string()),
  createdAt: v.number(),
  settledAt: v.optional(v.number()),
  nextAttemptAt: v.optional(v.number()),
  /** A script hook's run: the exit code, how long it took and the first kilobyte of its standard error. */
  run: v.optional(
    v.object({
      exitCode: v.optional(v.number()),
      durationMs: v.number(),
      stderr: v.string(),
    }),
  ),
});
export type HookDelivery = v.InferOutput<typeof HookDeliverySchema>;

/** GET /api/hooks/:id/deliveries: newest first; the body is never returned. */
export const HookDeliveriesResponseSchema = v.object({ deliveries: v.array(HookDeliverySchema) });
export type HookDeliveriesResponse = v.InferOutput<typeof HookDeliveriesResponseSchema>;

/** POST /api/hooks/:id/test: one `hook.test` event sent now and not retried. */
export const HookTestResponseSchema = v.object({
  hook: HookInfoSchema,
  delivery: HookDeliverySchema,
});
export type HookTestResponse = v.InferOutput<typeof HookTestResponseSchema>;
