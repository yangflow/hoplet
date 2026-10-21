import * as v from 'valibot';
import { SessionIdSchema } from '../ids.ts';
import { TrimmedSchema } from './common.ts';

/** What can notify (ADR 0021): a permission request, a plan review, a finished turn, a failure, a budget. */
export const NOTIFY_EVENT_KINDS = ['permission', 'plan', 'turn-done', 'failure', 'budget'] as const;
export const NotifyEventKindSchema = v.picklist(NOTIFY_EVENT_KINDS);
export type NotifyEventKind = v.InferOutput<typeof NotifyEventKindSchema>;

/** Where a notification goes: Web Push, the webhooks, connected native clients. */
export const NOTIFY_CHANNELS = ['push', 'webhook', 'native'] as const;
export const NotifyChannelSchema = v.picklist(NOTIFY_CHANNELS);
export type NotifyChannel = v.InferOutput<typeof NotifyChannelSchema>;

export const NOTIFY_PRESETS = ['needs-me', 'needs-me-and-done', 'everything', 'off'] as const;
export const NotifyPresetSchema = v.picklist(NOTIFY_PRESETS);
export type NotifyPreset = v.InferOutput<typeof NotifyPresetSchema>;

/** An override names the channels an event uses; none turns the event off. */
export const NotifyOverrideSchema = v.object({
  channels: v.pipe(v.array(NotifyChannelSchema), v.maxLength(NOTIFY_CHANNELS.length)),
});
export type NotifyOverride = v.InferOutput<typeof NotifyOverrideSchema>;

export const NotifyEventOverridesSchema = v.partial(
  v.object({
    permission: NotifyOverrideSchema,
    plan: NotifyOverrideSchema,
    'turn-done': NotifyOverrideSchema,
    failure: NotifyOverrideSchema,
    budget: NotifyOverrideSchema,
  }),
);
export type NotifyEventOverrides = v.InferOutput<typeof NotifyEventOverridesSchema>;

/** A project's own preset and event overrides; a project without a preset follows the global one. */
export const NotifyProjectPrefsSchema = v.object({
  preset: v.optional(NotifyPresetSchema),
  events: NotifyEventOverridesSchema,
});
export type NotifyProjectPrefs = v.InferOutput<typeof NotifyProjectPrefsSchema>;

const CLOCK_PATTERN = /^([01]\d|2[0-3]):[0-5]\d$/;

/** Quiet hours: `start` to `end` as `HH:MM` on the clock of the IANA zone `timeZone`; `start` after `end` spans midnight. */
export const QuietHoursSchema = v.object({
  start: v.pipe(v.string(), v.regex(CLOCK_PATTERN)),
  end: v.pipe(v.string(), v.regex(CLOCK_PATTERN)),
  timeZone: v.pipe(v.string(), v.minLength(1), v.maxLength(64)),
});
export type QuietHours = v.InferOutput<typeof QuietHoursSchema>;

/** The most projects with their own preferences. */
export const MAX_NOTIFY_PROJECTS = 200;

/** GET and PUT /api/notify/preferences */
export const NotifyPreferencesSchema = v.object({
  preset: NotifyPresetSchema,
  events: NotifyEventOverridesSchema,
  /** Keyed by the project path as sessions name it. */
  projects: v.record(
    v.pipe(v.string(), v.minLength(1), v.maxLength(4096)),
    NotifyProjectPrefsSchema,
  ),
  quietHours: v.nullable(QuietHoursSchema),
});
export type NotifyPreferences = v.InferOutput<typeof NotifyPreferencesSchema>;

/** GET /api/notify/push/key: the VAPID public key as unpadded base64url, the `applicationServerKey` of a subscription. */
export const PushKeyResponseSchema = v.object({ publicKey: v.string() });
export type PushKeyResponse = v.InferOutput<typeof PushKeyResponseSchema>;

const PUSH_ENDPOINT_MAX = 2048;

/** POST /api/notify/push/subscriptions: a browser's `PushSubscription` as JSON, with a label for the device. */
export const PushSubscribeRequestSchema = v.object({
  endpoint: v.pipe(v.string(), v.url(), v.maxLength(PUSH_ENDPOINT_MAX)),
  keys: v.object({
    p256dh: v.pipe(v.string(), v.minLength(1), v.maxLength(256)),
    auth: v.pipe(v.string(), v.minLength(1), v.maxLength(64)),
  }),
  label: v.pipe(TrimmedSchema, v.maxLength(80)),
});
export type PushSubscribeRequest = v.InferOutput<typeof PushSubscribeRequestSchema>;

/** POST /api/notify/push/unsubscribe */
export const PushUnsubscribeRequestSchema = v.object({
  endpoint: v.pipe(v.string(), v.url(), v.maxLength(PUSH_ENDPOINT_MAX)),
});
export type PushUnsubscribeRequest = v.InferOutput<typeof PushUnsubscribeRequestSchema>;

/** How the last send to a subscription or webhook went. */
export const NotifyDeliverySchema = v.object({
  /** Epoch milliseconds. */
  at: v.number(),
  ok: v.boolean(),
  /** Why it failed: `timeout`, `gone`, `http-<status>` or `error`. */
  error: v.optional(v.string()),
});
export type NotifyDelivery = v.InferOutput<typeof NotifyDeliverySchema>;

export const PushSubscriptionInfoSchema = v.object({
  id: v.string(),
  label: v.string(),
  /** Only the host of the push service, never the endpoint, which is a credential of its own. */
  service: v.string(),
  createdAt: v.number(),
  lastDelivery: v.optional(NotifyDeliverySchema),
});
export type PushSubscriptionInfo = v.InferOutput<typeof PushSubscriptionInfoSchema>;

export const PushSubscriptionListResponseSchema = v.object({
  subscriptions: v.array(PushSubscriptionInfoSchema),
});
export type PushSubscriptionListResponse = v.InferOutput<typeof PushSubscriptionListResponseSchema>;

const WEBHOOK_URL_MAX = 2048;
const WEBHOOK_SECRET_MAX = 256;

/** The most webhooks. */
export const MAX_WEBHOOKS = 20;

/** The events a webhook receives when none are chosen: the ones where Claude needs the user, and budgets. */
export const DEFAULT_WEBHOOK_EVENTS = [
  'permission',
  'plan',
  'failure',
  'budget',
] as const satisfies readonly NotifyEventKind[];

/** The events one webhook receives: a non-empty set of kinds, each once. */
export const WebhookEventsSchema = v.pipe(
  v.array(NotifyEventKindSchema),
  v.minLength(1),
  v.maxLength(NOTIFY_EVENT_KINDS.length),
  v.check((kinds) => new Set(kinds).size === kinds.length, 'Events must not repeat'),
);

/** POST /api/notify/webhooks */
export const WebhookCreateRequestSchema = v.object({
  url: v.pipe(v.string(), v.url(), v.maxLength(WEBHOOK_URL_MAX), v.regex(/^https?:\/\//)),
  label: v.pipe(TrimmedSchema, v.maxLength(80)),
  /** Signs the body; write-only, never returned. */
  secret: v.optional(v.pipe(v.string(), v.minLength(1), v.maxLength(WEBHOOK_SECRET_MAX))),
  /** The events this webhook receives; permission, plan and failure when absent. */
  events: v.optional(WebhookEventsSchema),
});
export type WebhookCreateRequest = v.InferOutput<typeof WebhookCreateRequestSchema>;

/** PATCH /api/notify/webhooks/:id; a `secret` of `null` removes the secret. */
export const WebhookUpdateRequestSchema = v.object({
  url: v.optional(
    v.pipe(v.string(), v.url(), v.maxLength(WEBHOOK_URL_MAX), v.regex(/^https?:\/\//)),
  ),
  label: v.optional(v.pipe(TrimmedSchema, v.maxLength(80))),
  secret: v.optional(
    v.nullable(v.pipe(v.string(), v.minLength(1), v.maxLength(WEBHOOK_SECRET_MAX))),
  ),
  enabled: v.optional(v.boolean()),
  events: v.optional(WebhookEventsSchema),
});
export type WebhookUpdateRequest = v.InferOutput<typeof WebhookUpdateRequestSchema>;

export const WebhookInfoSchema = v.object({
  id: v.string(),
  label: v.string(),
  url: v.string(),
  /** Whether a signing secret is set; the secret itself is never returned. */
  hasSecret: v.boolean(),
  enabled: v.boolean(),
  /** The events this webhook receives, in the order of `NOTIFY_EVENT_KINDS`. */
  events: WebhookEventsSchema,
  createdAt: v.number(),
  lastDelivery: v.optional(NotifyDeliverySchema),
});
export type WebhookInfo = v.InferOutput<typeof WebhookInfoSchema>;

export const WebhookListResponseSchema = v.object({ webhooks: v.array(WebhookInfoSchema) });
export type WebhookListResponse = v.InferOutput<typeof WebhookListResponseSchema>;

/** POST /api/notify/webhooks/:id/test: the webhook after a test send, with its delivery status. */
export const WebhookTestResponseSchema = v.object({
  webhook: WebhookInfoSchema,
  delivery: NotifyDeliverySchema,
});
export type WebhookTestResponse = v.InferOutput<typeof WebhookTestResponseSchema>;

/** The payload of a notification on every channel (ADR 0021): never the prompt, file contents or a diff. */
export const NotifyPayloadSchema = v.object({
  kind: NotifyEventKindSchema,
  /** Absent for a session that failed to start: it does not exist. */
  sessionId: v.optional(SessionIdSchema),
  project: v.string(),
  /** The session's title; absent for a session that failed to start. */
  title: v.optional(v.string()),
  tool: v.optional(v.string()),
  /** The redacted command or path, at most 80 characters. */
  summary: v.optional(v.string()),
  requestId: v.optional(v.string()),
  /** Why the failure happened: a start or handoff error code. */
  reason: v.optional(v.string()),
  /** A budget notification: which budget, which line it crossed and how far spend is. */
  budget: v.optional(
    v.object({
      id: v.string(),
      name: v.string(),
      threshold: v.picklist(['alert', 'limit']),
      percent: v.number(),
      pause: v.boolean(),
    }),
  ),
  /** The action link of a permission or plan notification; absent on a channel that does not carry links. */
  link: v.optional(v.string()),
});
export type NotifyPayload = v.InferOutput<typeof NotifyPayloadSchema>;

/** The signed token of an action link: `<payload>.<mac>` in base64url. */
export const ActionTokenSchema = v.pipe(
  v.string(),
  v.regex(/^[A-Za-z0-9_-]{8,1024}\.[A-Za-z0-9_-]{8,128}$/),
);

/** POST /api/action/inspect */
export const ActionInspectRequestSchema = v.object({ token: ActionTokenSchema });
export type ActionInspectRequest = v.InferOutput<typeof ActionInspectRequestSchema>;

/**
 * What the action page shows about a request: `open` can be decided here; `answered` was decided by another
 * window or device; `expired` is a request or token whose time is over; `used` is a link that was already used;
 * `webui-only` is a request that can only be approved in the web UI.
 */
export const ACTION_STATES = ['open', 'answered', 'expired', 'used', 'webui-only'] as const;
export const ActionStateSchema = v.picklist(ACTION_STATES);
export type ActionState = v.InferOutput<typeof ActionStateSchema>;

export const ActionInspectResponseSchema = v.object({
  state: ActionStateSchema,
  sessionId: v.optional(SessionIdSchema),
  requestId: v.optional(v.string()),
  /** `permission` or `plan`. */
  kind: v.optional(v.picklist(['permission', 'plan'])),
  project: v.optional(v.string()),
  title: v.optional(v.string()),
  tool: v.optional(v.string()),
  /** The redacted command or path, longer than a notification carries. */
  summary: v.optional(v.string()),
  /** The mode approving a plan switches to. */
  planMode: v.optional(v.string()),
  /** The plan as Markdown, for a plan review. */
  plan: v.optional(v.string()),
  /** Epoch milliseconds when the link stops working. */
  expiresAt: v.optional(v.number()),
});
export type ActionInspectResponse = v.InferOutput<typeof ActionInspectResponseSchema>;

/** POST /api/action/decide */
export const ActionDecideRequestSchema = v.object({
  token: ActionTokenSchema,
  decision: v.picklist(['allow', 'deny']),
});
export type ActionDecideRequest = v.InferOutput<typeof ActionDecideRequestSchema>;

/** The result of a decision: `decided` when this call answered, otherwise why it did not. */
export const ActionDecideResponseSchema = v.object({
  state: v.picklist(['decided', 'answered', 'expired', 'used', 'webui-only']),
});
export type ActionDecideResponse = v.InferOutput<typeof ActionDecideResponseSchema>;
