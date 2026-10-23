import * as v from 'valibot';
import { TrimmedSchema } from './common.ts';

/**
 * How far a session reaches over MCP (ADR 0034): `project` the sessions of its own watched project on its own
 * machine (worktrees included), `machine` every session of its machine, `all` every session Hoplet knows.
 */
export const SESSION_VISIBILITIES = ['project', 'machine', 'all'] as const;
export const SessionVisibilitySchema = v.picklist(SESSION_VISIBILITIES);
export type SessionVisibility = v.InferOutput<typeof SessionVisibilitySchema>;

/** The longest chain of sessions that direct each other: A directs B directs C directs D, and no further. */
export const MAX_SESSION_CHAIN = 3;

/** Who wrote a prompt that another session sent (ADR 0034): set by the core from the token, never by the sender. */
export const PromptSourceSchema = v.strictObject({
  kind: v.literal('session'),
  sessionId: v.string(),
  /** The sender's title, cut and cleaned by the core; the target's agent quotes it in the first line. */
  title: v.pipe(v.string(), v.maxLength(200)),
  /** The sessions behind the prompt, the sender last. */
  chain: v.pipe(v.array(v.string()), v.maxLength(MAX_SESSION_CHAIN + 1)),
});
export type PromptSource = v.InferOutput<typeof PromptSourceSchema>;

/** The most machines one session may name for automatic hand-off approval. */
export const MAX_AUTO_APPROVE_MACHINES = 32;

/**
 * What one session may do to other sessions (ADR 0034). Reading is always on. `send` allows `send_prompt` and
 * `interrupt_session`; `create` allows `start_session` and implies `send`; `requestHandoff` allows asking the user
 * to hand the session off. `autoApproveHandoff` lets those requests from this session pass without the user's
 * confirmation when the destination is one of `autoApproveMachines`; it is only meaningful with `requestHandoff`.
 * `readConfig` allows reading the session's own configuration and `writeConfig` allows proposing changes to it
 * (ADR 0036); `writeConfig` implies `readConfig`, and every change stays the user's to approve.
 * Answering permission requests is not an ability a session can be given.
 */
export const SessionMcpSettingsSchema = v.strictObject({
  send: v.boolean(),
  create: v.boolean(),
  requestHandoff: v.boolean(),
  autoApproveHandoff: v.boolean(),
  readConfig: v.boolean(),
  writeConfig: v.boolean(),
  autoApproveMachines: v.pipe(
    v.array(v.pipe(TrimmedSchema, v.maxLength(128))),
    v.maxLength(MAX_AUTO_APPROVE_MACHINES),
  ),
  visibility: SessionVisibilitySchema,
});
export type SessionMcpSettings = v.InferOutput<typeof SessionMcpSettingsSchema>;

/** What a session gets when neither its project nor the global default says otherwise: read only, its project. */
export const DEFAULT_SESSION_MCP_SETTINGS: SessionMcpSettings = {
  send: false,
  create: false,
  requestHandoff: false,
  autoApproveHandoff: false,
  readConfig: true,
  writeConfig: false,
  autoApproveMachines: [],
  visibility: 'project',
};

/** GET and PUT /api/sessions/:sessionId/mcp */
export const SessionMcpResponseSchema = v.object({
  sessionId: v.string(),
  settings: SessionMcpSettingsSchema,
});
export type SessionMcpResponse = v.InferOutput<typeof SessionMcpResponseSchema>;

/** PUT /api/sessions/:sessionId/mcp: the whole settings; `create` without `send` is stored as both. */
export const SessionMcpPutRequestSchema = v.strictObject({ settings: SessionMcpSettingsSchema });
export type SessionMcpPutRequest = v.InferOutput<typeof SessionMcpPutRequestSchema>;

/** A project's default for the sessions started in it from now on. */
export const SessionMcpProjectDefaultSchema = v.object({
  deviceId: v.string(),
  path: v.string(),
  settings: SessionMcpSettingsSchema,
});
export type SessionMcpProjectDefault = v.InferOutput<typeof SessionMcpProjectDefaultSchema>;

/** GET /api/mcp/defaults */
export const SessionMcpDefaultsResponseSchema = v.object({
  global: SessionMcpSettingsSchema,
  projects: v.array(SessionMcpProjectDefaultSchema),
});
export type SessionMcpDefaultsResponse = v.InferOutput<typeof SessionMcpDefaultsResponseSchema>;

/**
 * PUT /api/mcp/defaults: the global default, or, with `deviceId` and `path` together (one without the other is `invalid-request`), one project's. Only sessions
 * that begin afterwards take it; sessions that already have settings keep theirs.
 */
export const SessionMcpDefaultsPutRequestSchema = v.strictObject({
  deviceId: v.optional(v.pipe(TrimmedSchema, v.maxLength(128))),
  path: v.optional(v.pipe(TrimmedSchema, v.maxLength(4096))),
  settings: SessionMcpSettingsSchema,
});
export type SessionMcpDefaultsPutRequest = v.InferOutput<typeof SessionMcpDefaultsPutRequestSchema>;

/** DELETE /api/mcp/defaults?deviceId=&path= removes one project's default. */
export const SessionMcpDefaultsDeleteQuerySchema = v.object({
  deviceId: v.pipe(TrimmedSchema, v.maxLength(128)),
  path: v.pipe(TrimmedSchema, v.maxLength(4096)),
});
export type SessionMcpDefaultsDeleteQuery = v.InferOutput<
  typeof SessionMcpDefaultsDeleteQuerySchema
>;

/** The answer of the routes that change or remove a default. */
export const SessionMcpOkResponseSchema = v.object({ ok: v.literal(true) });

/** How long a hand-off request waits for the user (ADR 0034). */
export const HANDOFF_REQUEST_TTL_MS = 24 * 60 * 60 * 1000;

/** The most characters of the reason a session gives for a hand-off request. */
export const MAX_HANDOFF_REASON_CHARS = 500;

/**
 * Where a hand-off request is: `pending` waits for the user, `approved` (by the user or automatically) waits for
 * the end of the turn and then runs, `declined` and `expired` are closed without a hand-off, `done` moved the
 * session and `failed` did not (`error` names why).
 */
export const HANDOFF_REQUEST_STATES = [
  'pending',
  'approved',
  'declined',
  'expired',
  'done',
  'failed',
] as const;
export const HandoffRequestStateSchema = v.picklist(HANDOFF_REQUEST_STATES);
export type HandoffRequestState = v.InferOutput<typeof HandoffRequestStateSchema>;

/** Who approved or declined: the user, or the session's own automatic approval. */
export const HANDOFF_DECISIONS = ['user', 'auto'] as const;
export const HandoffDecisionSchema = v.picklist(HANDOFF_DECISIONS);
export type HandoffDecision = v.InferOutput<typeof HandoffDecisionSchema>;

/** A session's request to be handed off to another machine (ADR 0034). */
export const SessionHandoffRequestSchema = v.object({
  requestId: v.string(),
  sessionId: v.string(),
  targetDeviceId: v.string(),
  reason: v.string(),
  state: HandoffRequestStateSchema,
  /** Null while `pending`, and after `expired`. */
  decision: v.nullable(HandoffDecisionSchema),
  createdAt: v.number(),
  /** When a request nobody answered expires. */
  expiresAt: v.number(),
  decidedAt: v.nullable(v.number()),
  /** The error code of a `failed` request (`no-clone`, `clone-failed`, `target-offline` and so on). */
  error: v.optional(v.string()),
  errorReason: v.optional(v.string()),
});
export type SessionHandoffRequest = v.InferOutput<typeof SessionHandoffRequestSchema>;

/** POST /api/handoff-requests, with a session token: `targetDeviceId` is a machine ID from `list_machines`. */
export const SessionHandoffRequestCreateSchema = v.strictObject({
  targetDeviceId: v.pipe(TrimmedSchema, v.maxLength(128)),
  reason: v.pipe(TrimmedSchema, v.maxLength(MAX_HANDOFF_REASON_CHARS)),
});
export type SessionHandoffRequestCreate = v.InferOutput<typeof SessionHandoffRequestCreateSchema>;

/** GET /api/sessions/:sessionId/handoff-requests: the newest first. */
export const SessionHandoffRequestListSchema = v.object({
  requests: v.array(SessionHandoffRequestSchema),
});
export type SessionHandoffRequestList = v.InferOutput<typeof SessionHandoffRequestListSchema>;
