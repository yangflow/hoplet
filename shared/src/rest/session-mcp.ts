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
 * Answering permission requests is not an ability a session can be given.
 */
export const SessionMcpSettingsSchema = v.strictObject({
  send: v.boolean(),
  create: v.boolean(),
  requestHandoff: v.boolean(),
  autoApproveHandoff: v.boolean(),
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
