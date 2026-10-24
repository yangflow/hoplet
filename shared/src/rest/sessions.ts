import * as v from 'valibot';
import { MAX_BRANCH_NAME_LENGTH } from '../branch.ts';
import { EntrySchema } from '../conversation.ts';
import { SessionIdSchema } from '../ids.ts';
import {
  PermissionActionSchema,
  PermissionMatchSchema,
  PermissionNoteSchema,
  PermissionToolSchema,
} from '../permission-rules.ts';
import {
  ConfigChangeSchema,
  ConfigurationIdSchema,
  PromptAttachmentsSchema,
  RuntimeCapabilitiesSchema,
  RuntimeStateSchema,
  SessionConfigSchema,
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
 * `offline` when the owner is not connected; `capabilities` once the owner has reported them,
 * with the session's values in `capabilities.config`.
 */
export const SessionRuntimeInfoSchema = v.object({
  state: RuntimeStateSchema,
  capabilities: v.optional(RuntimeCapabilitiesSchema),
  error: v.optional(v.string()),
});
export type SessionRuntimeInfo = v.InferOutput<typeof SessionRuntimeInfoSchema>;

/** The longest title a user gives a session. */
export const MAX_TITLE_OVERRIDE_CHARS = 200;

/**
 * What the user did to a session in Hoplet, beside what its vendor wrote. An absent field means nothing
 * was done: no title of their own, not pinned (`pinnedAt` is when it was), not archived (`archivedAt`).
 */
export const SessionStateSchema = v.object({
  titleOverride: v.optional(v.string()),
  pinnedAt: v.optional(v.number()),
  archivedAt: v.optional(v.number()),
});
export type SessionState = v.InferOutput<typeof SessionStateSchema>;

/** The other end of a plan approved into a new session (ADR 0025); `title` is empty while that session has none to show. */
export const SessionContinuationSchema = v.object({
  sessionId: SessionIdSchema,
  title: v.string(),
});
export type SessionContinuation = v.InferOutput<typeof SessionContinuationSchema>;

/** A session as the REST API returns it: the index entry, whether its owner agent is connected, its runtime state, the user's changes and its continuation links. */
export const LiveSessionMetaSchema = v.object({
  ...SessionMetaSchema.entries,
  /**
   * The provider configuration the session belongs to (ADR 0028), named as its owner machine names it and by its
   * ID while that machine is offline; absent for the vendor's own.
   */
  configuration: v.optional(v.object({ id: ConfigurationIdSchema, name: v.string() })),
  ownerConnected: v.boolean(),
  runtime: SessionRuntimeInfoSchema,
  ...SessionStateSchema.entries,
  continuedIn: v.optional(SessionContinuationSchema),
  continuedFrom: v.optional(SessionContinuationSchema),
  /** The session whose latest prompt this session's provenance names last (ADR 0035); `null` when the latest prompt came from a user. */
  parentId: v.nullable(v.string()),
  /** How many sessions have this session as `parentId`. */
  childCount: v.pipe(v.number(), v.integer(), v.minValue(0)),
  /** Present (`true`) only when a session token asks: this entry is the asking session itself (ADR 0034). */
  self: v.optional(v.literal(true)),
});
export type LiveSessionMeta = v.InferOutput<typeof LiveSessionMetaSchema>;

/** GET /api/sessions */
export const SessionListResponseSchema = v.object({ sessions: v.array(LiveSessionMetaSchema) });
export type SessionListResponse = v.InferOutput<typeof SessionListResponseSchema>;

/** The most descendants GET /api/sessions/:id/children returns. */
export const MAX_CHILD_TREE = 200;

/** One descendant: the session plus its depth below the asked session (1 for a child). */
export const SessionDescendantSchema = v.object({
  ...LiveSessionMetaSchema.entries,
  depth: v.pipe(v.number(), v.integer(), v.minValue(1)),
});
export type SessionDescendant = v.InferOutput<typeof SessionDescendantSchema>;

/**
 * GET /api/sessions/:id/children: the descendants of a session as a flat list in pre-order (a parent before its
 * children, siblings by creation); `parentId` rebuilds the tree. `truncated` is true when more than
 * `MAX_CHILD_TREE` exist or the token's grant leaves some out of the list.
 */
export const SessionChildrenResponseSchema = v.object({
  sessionId: SessionIdSchema,
  descendants: v.array(SessionDescendantSchema),
  truncated: v.boolean(),
});
export type SessionChildrenResponse = v.InferOutput<typeof SessionChildrenResponseSchema>;

/**
 * PATCH /api/sessions/:id: changes what the user did to the session; a field left out stays as it is.
 * `title` is the session's name in Hoplet: the core stores it trimmed, as `null` when it is blank or the vendor's own
 * title, and refuses more than `MAX_TITLE_OVERRIDE_CHARS` characters after trimming (`invalid-request`). `pinned` and
 * `archived` set or clear the flags; setting one that is set keeps its time. A request that names none of them is
 * `invalid-request`.
 */
export const SessionPatchRequestSchema = v.object({
  title: v.optional(v.nullable(v.string())),
  pinned: v.optional(v.boolean()),
  archived: v.optional(v.boolean()),
});
export type SessionPatchRequest = v.InferOutput<typeof SessionPatchRequestSchema>;

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
  /** Images uploaded to the owner for this prompt; the session's driver must declare `images`. */
  attachments: v.optional(PromptAttachmentsSchema),
  /** Chosen by the client; a repeated one within 10 minutes returns the first result. */
  promptId: v.pipe(v.string(), v.minLength(1), v.maxLength(128)),
});
export type PromptRequest = v.InferOutput<typeof PromptRequestSchema>;

export const PromptResultSchema = v.object({ ok: v.literal(true), promptId: v.string() });
export type PromptResult = v.InferOutput<typeof PromptResultSchema>;

/** POST /api/sessions/:id/config: sets one setting of the session; the session's driver must declare it. */
export const ConfigRequestSchema = ConfigChangeSchema;
export type ConfigRequest = v.InferOutput<typeof ConfigRequestSchema>;

/**
 * POST /api/sessions: starts a session in a watched project of a connected device. `config` holds the settings
 * to start with, only what the provider's capabilities declare; an option left out keeps its default. The response comes when the agent has created and imported the session.
 */
export const NewSessionRequestSchema = v.object({
  deviceId: TrimmedSchema,
  projectPath: v.pipe(TrimmedSchema, v.maxLength(4096)),
  provider: ProviderSchema,
  /** The provider configuration of the device to start with (ADR 0028); the vendor's own when absent. */
  configuration: v.optional(ConfigurationIdSchema),
  config: v.optional(SessionConfigSchema),
  text: TrimmedSchema,
  /** Images uploaded to the device for the first prompt; the provider's driver must declare `images`. */
  attachments: v.optional(PromptAttachmentsSchema),
  /** Chosen by the client; a repeated one within 10 minutes returns the first result. */
  promptId: v.pipe(v.string(), v.minLength(1), v.maxLength(128)),
  /**
   * Run the session in a new git worktree of the project's repository: `branch` names the new branch (git
   * branch name rules, see `branchNameProblem`), absent the agent generates one. `{}` asks for a generated one.
   * `false` asks for none: it is the same as leaving `worktree` out, except for a session that starts a child,
   * where leaving it out means a new worktree when the project is a git repository (ADR 0035).
   */
  worktree: v.optional(
    v.union([
      v.object({ branch: v.optional(v.pipe(v.string(), v.maxLength(MAX_BRANCH_NAME_LENGTH))) }),
      v.literal(false),
    ]),
  ),
  /** The title the session list shows, as if the user had renamed the session; at most `MAX_TITLE_OVERRIDE_CHARS` characters. */
  title: v.optional(v.pipe(TrimmedSchema, v.maxLength(MAX_TITLE_OVERRIDE_CHARS))),
  /**
   * The new session may start sessions of its own, within the depth limit (ADR 0035). Only a session's token may
   * ask; any other caller gets `invalid-request`.
   */
  nested: v.optional(v.boolean()),
  /**
   * How long a child session may run, in minutes (1 to 120; the core's default is 30). Only a session's token may
   * ask; any other caller gets `invalid-request`.
   */
  timeLimitMinutes: v.optional(v.pipe(v.number(), v.integer(), v.minValue(1), v.maxValue(120))),
});
export type NewSessionRequest = v.InferOutput<typeof NewSessionRequestSchema>;

export const NewSessionResultSchema = v.object({
  ok: v.literal(true),
  sessionId: SessionIdSchema,
  promptId: v.string(),
  /** The worktree made for the session, when one was asked for. */
  worktree: v.optional(v.object({ path: v.string(), branch: v.string() })),
});
export type NewSessionResult = v.InferOutput<typeof NewSessionResultSchema>;

/**
 * POST /api/sessions/:id/handoff. `targetPath` names the clone to use when the target has no watched project
 * with the same `origin`. The response comes when the handoff is over; `handoff.progress` reports the steps.
 */
export const HandoffRequestSchema = v.object({
  targetDeviceId: TrimmedSchema,
  targetPath: v.optional(v.pipe(TrimmedSchema, v.maxLength(4096))),
  /** Clone the repository here on the target first (ADR 0024); exclusive with `targetPath`, which the route checks. */
  cloneInto: v.optional(v.pipe(TrimmedSchema, v.maxLength(4096))),
  /**
   * The target's provider configuration to continue under (ADR 0028): enabled on the target and extending the
   * session's provider, else `invalid-request`. Absent means the vendor's own.
   */
  configuration: v.optional(ConfigurationIdSchema),
  /**
   * Copy the source project's rules that the target's clone lacks to it before the commit (ADR 0037). Only the
   * owner sets it; a session's own handoff request never copies.
   */
  copyProjectRules: v.optional(v.boolean()),
});
export type HandoffRequest = v.InferOutput<typeof HandoffRequestSchema>;

/** GET /api/sessions/:id/handoff/rules: the target machine and the clone there the rules would be copied to. */
export const HandoffRulesQuerySchema = v.strictObject({
  targetDeviceId: TrimmedSchema,
  targetPath: v.pipe(TrimmedSchema, v.minLength(1), v.maxLength(4096)),
});
export type HandoffRulesQuery = v.InferOutput<typeof HandoffRulesQuerySchema>;

/**
 * One rule of the copy a handoff would make: `copy` the target lacks it and the handoff adds it (its paths moved
 * onto the target's clone), `same` the target has it, `target` the target's own rule the source does not have.
 */
export const HandoffRuleSchema = v.object({
  state: v.picklist(['copy', 'same', 'target']),
  action: PermissionActionSchema,
  tool: PermissionToolSchema,
  match: PermissionMatchSchema,
  note: PermissionNoteSchema,
});
export type HandoffRule = v.InferOutput<typeof HandoffRuleSchema>;

/** The answer of GET /api/sessions/:id/handoff/rules. `sourcePath` is null when the session has no project. */
export const HandoffRulesPreviewSchema = v.object({
  sourceDeviceId: v.string(),
  sourcePath: v.nullable(v.string()),
  targetDeviceId: v.string(),
  targetPath: v.string(),
  rules: v.array(HandoffRuleSchema),
});
export type HandoffRulesPreview = v.InferOutput<typeof HandoffRulesPreviewSchema>;

export const HandoffResultSchema = v.object({
  ok: v.literal(true),
  ownerDeviceId: v.string(),
  ownerEpoch: v.number(),
  /** The worktree the session continues in on the new owner, and its branch. */
  worktree: v.string(),
  branch: v.string(),
  /** The time of the snapshot an offline handoff installed; absent when the owner was online. */
  snapshotAt: v.optional(v.number()),
  /** The project rules copied to the target (ADR 0037); absent when none were. */
  rulesCopied: v.optional(v.number()),
});
export type HandoffResult = v.InferOutput<typeof HandoffResultSchema>;

/** POST /api/sessions/:id/handoff/cancel: stops a handoff still in its clone step. */
export const HandoffCancelResultSchema = v.object({ ok: v.literal(true) });
export type HandoffCancelResult = v.InferOutput<typeof HandoffCancelResultSchema>;
