import * as v from 'valibot';
import { EntrySchema } from '../conversation.ts';
import { SessionIdSchema } from '../ids.ts';
import {
  PromptImagesSchema,
  RuntimeCapabilitiesSchema,
  RuntimeStateSchema,
  StartOptionsSchema,
} from '../runtime.ts';
import { ProviderSchema, SessionMetaSchema } from '../session.ts';
import { QuerySchema, queryInt, TrimmedSchema } from './common.ts';

/** GET /api/health */
export const HealthResponseSchema = v.object({
  ok: v.literal(true),
  sessions: v.number(),
  uptimeSec: v.number(),
  /** The product version the core was built from (the repository `VERSION`). */
  coreVersion: v.string(),
  /** The `v` of the WebSocket protocol the core speaks (`PROTOCOL_VERSION`). */
  protocolVersion: v.number(),
});
export type HealthResponse = v.InferOutput<typeof HealthResponseSchema>;

/**
 * The runtime state of a session as browsers see it (the same values `session.runtime` carries):
 * `offline` when the owner is not connected; `capabilities` once the owner has reported them.
 */
export const SessionRuntimeInfoSchema = v.object({
  state: RuntimeStateSchema,
  capabilities: v.optional(RuntimeCapabilitiesSchema),
  error: v.optional(v.string()),
});
export type SessionRuntimeInfo = v.InferOutput<typeof SessionRuntimeInfoSchema>;

/** A session as the REST API returns it: the index entry, whether its owner agent is connected, and its runtime state. */
export const LiveSessionMetaSchema = v.object({
  ...SessionMetaSchema.entries,
  ownerConnected: v.boolean(),
  runtime: SessionRuntimeInfoSchema,
});
export type LiveSessionMeta = v.InferOutput<typeof LiveSessionMetaSchema>;

/** GET /api/sessions */
export const SessionListResponseSchema = v.object({ sessions: v.array(LiveSessionMetaSchema) });
export type SessionListResponse = v.InferOutput<typeof SessionListResponseSchema>;

/** GET /api/sessions/:id/lines?start=&limit= (negative `start` counts from the end) */
export const LinesQuerySchema = v.object({
  start: queryInt(-100),
  limit: queryInt(100, 1, 500),
});
export type LinesQuery = v.InferOutput<typeof LinesQuerySchema>;

/**
 * A page of the conversation: the stored lines [start, end) as entries of the conversation model. A line
 * that holds no conversation (vendor bookkeeping) has no entry, so `entries` may be shorter than the range.
 */
export const LinesPageSchema = v.object({
  sessionId: SessionIdSchema,
  start: v.number(),
  end: v.number(),
  total: v.number(),
  entries: v.array(EntrySchema),
});
export type LinesPage = v.InferOutput<typeof LinesPageSchema>;

/** GET /api/sessions/:id/blocks/:line/:block: path parameters, as non-negative integers */
export const BlockParamsSchema = v.object({
  line: v.pipe(v.string(), v.regex(/^\d{1,12}$/), v.transform(Number)),
  block: v.pipe(v.string(), v.regex(/^\d{1,6}$/), v.transform(Number)),
});

/** GET /api/sessions/:id/search?q=&start=&limit= */
export const SessionSearchQuerySchema = v.object({
  q: QuerySchema,
  start: queryInt(0, 0),
  limit: queryInt(50, 1, 200),
});

/** A line that matched a search query, with a short text excerpt of its entry */
export const SearchHitSchema = v.object({ lineNo: v.number(), excerpt: v.string() });
export type SearchHit = v.InferOutput<typeof SearchHitSchema>;

export const SearchResultSchema = v.object({
  sessionId: SessionIdSchema,
  query: v.string(),
  start: v.number(),
  total: v.number(),
  hits: v.array(SearchHitSchema),
});
export type SearchResult = v.InferOutput<typeof SearchResultSchema>;

/** GET /api/search?q=&start=&limit= */
export const GlobalSearchQuerySchema = v.object({
  q: QuerySchema,
  start: queryInt(0, 0),
  limit: queryInt(30, 1, 100),
});

/** One line that matched a global search */
export const GlobalSearchHitSchema = v.object({
  sessionId: SessionIdSchema,
  lineNo: v.number(),
  excerpt: v.string(),
});
export type GlobalSearchHit = v.InferOutput<typeof GlobalSearchHitSchema>;

/** `exhausted: false` means more sessions or hits may exist */
export const GlobalSearchResultSchema = v.object({
  query: v.string(),
  start: v.number(),
  results: v.array(GlobalSearchHitSchema),
  exhausted: v.boolean(),
});
export type GlobalSearchResult = v.InferOutput<typeof GlobalSearchResultSchema>;

/** POST /api/sessions/:id/prompt */
export const PromptRequestSchema = v.object({
  text: TrimmedSchema,
  /** Pasted or attached images; the session's driver must declare `images`. */
  images: v.optional(PromptImagesSchema),
  /** Chosen by the client; a repeated one within 10 minutes returns the first result. */
  promptId: v.pipe(v.string(), v.minLength(1), v.maxLength(128)),
});
export type PromptRequest = v.InferOutput<typeof PromptRequestSchema>;

export const PromptResultSchema = v.object({ ok: v.literal(true), promptId: v.string() });
export type PromptResult = v.InferOutput<typeof PromptResultSchema>;

/**
 * POST /api/sessions: starts a session in a watched project of a connected device. `options` holds only what
 * the provider's capabilities declare. The response comes when the agent has created and imported the session.
 */
export const NewSessionRequestSchema = v.object({
  deviceId: TrimmedSchema,
  projectPath: v.pipe(TrimmedSchema, v.maxLength(4096)),
  provider: ProviderSchema,
  options: StartOptionsSchema,
  text: TrimmedSchema,
  /** Images of the first prompt; the provider's driver must declare `images`. */
  images: v.optional(PromptImagesSchema),
  /** Chosen by the client; a repeated one within 10 minutes returns the first result. */
  promptId: v.pipe(v.string(), v.minLength(1), v.maxLength(128)),
});
export type NewSessionRequest = v.InferOutput<typeof NewSessionRequestSchema>;

export const NewSessionResultSchema = v.object({
  ok: v.literal(true),
  sessionId: SessionIdSchema,
  promptId: v.string(),
});
export type NewSessionResult = v.InferOutput<typeof NewSessionResultSchema>;

/**
 * POST /api/sessions/:id/handoff. `targetPath` names the clone to use when the target has no watched project
 * with the same `origin`. The response comes when the handoff is over; `handoff.progress` reports the steps.
 */
export const HandoffRequestSchema = v.object({
  targetDeviceId: TrimmedSchema,
  targetPath: v.optional(v.pipe(TrimmedSchema, v.maxLength(4096))),
});
export type HandoffRequest = v.InferOutput<typeof HandoffRequestSchema>;

export const HandoffResultSchema = v.object({
  ok: v.literal(true),
  ownerDeviceId: v.string(),
  ownerEpoch: v.number(),
  /** The worktree the session continues in on the new owner, and its branch. */
  worktree: v.string(),
  branch: v.string(),
  /** The time of the snapshot an offline handoff installed; absent when the owner was online. */
  snapshotAt: v.optional(v.number()),
});
export type HandoffResult = v.InferOutput<typeof HandoffResultSchema>;
