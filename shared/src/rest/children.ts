import * as v from 'valibot';
import { SessionIdSchema } from '../ids.ts';
import { TrimmedSchema } from './common.ts';
import { UsageTotalsSchema } from './usage.ts';
import { WorkspaceChangeSchema } from './workspace.ts';

/** The most bytes of a child's report `collect` returns (ADR 0035). */
export const MAX_CHILD_SUMMARY_BYTES = 6 * 1024;
/** The most bytes the whole answer of `collect` holds. */
export const MAX_CHILD_COLLECT_BYTES = 8 * 1024;
/** The most files `collect` lists. */
export const MAX_CHILD_COLLECT_FILES = 200;
/** The most bytes of hunks one page of a child's diff holds. */
export const MAX_CHILD_DIFF_BYTES = 32 * 1024;

/**
 * Where a child stands for its parent: `running` (starting, running or between turns of a start), `done` (the
 * runtime reached `ready`), `failed`, `blocked` (waiting for a permission), `ended` (stopped, or its machine is
 * gone), `timed-out` (a limit of the tree stopped it) and `cancelled` (its parent cancelled it).
 */
export const CHILD_STATES = [
  'running',
  'done',
  'failed',
  'blocked',
  'ended',
  'timed-out',
  'cancelled',
] as const;
export const ChildStateSchema = v.picklist(CHILD_STATES);
export type ChildState = v.InferOutput<typeof ChildStateSchema>;

export const ChildStatusSchema = v.object({
  sessionId: SessionIdSchema,
  state: ChildStateSchema,
  /** Seconds since the core started the child. */
  elapsedSeconds: v.number(),
});
export type ChildStatus = v.InferOutput<typeof ChildStatusSchema>;

/** `ids` narrows the answer to those children (comma separated); one that is no child of the session is `session-not-found`. */
export const ChildStatusQuerySchema = v.object({
  ids: v.optional(v.pipe(v.string(), v.maxLength(1024))),
});
export type ChildStatusQuery = v.InferOutput<typeof ChildStatusQuerySchema>;

/** GET /api/sessions/:id/children/status?ids=: the states of the direct children of the session, by creation. */
export const ChildStatusesSchema = v.object({
  sessionId: SessionIdSchema,
  children: v.array(ChildStatusSchema),
});
export type ChildStatuses = v.InferOutput<typeof ChildStatusesSchema>;

/**
 * What a child changed, as far as it is known: `ok` with the files, `unknown` when its directory is no git
 * repository (or the owner could not tell), `owner-offline` when its machine is not connected.
 */
export const ChildChangesSchema = v.object({
  status: v.picklist(['ok', 'unknown', 'owner-offline']),
  branch: v.optional(v.string()),
  base: v.optional(v.string()),
  /** True when the child has no worktree of its own: the files are everything uncommitted in the shared directory. */
  shared: v.optional(v.boolean()),
  total: v.optional(v.number()),
  files: v.optional(v.array(WorkspaceChangeSchema)),
  /** True when `files` lists fewer than `total` paths. */
  truncated: v.optional(v.boolean()),
});
export type ChildChanges = v.InferOutput<typeof ChildChangesSchema>;

export const ChildCollectQuerySchema = v.object({
  release: v.optional(v.picklist(['true', 'false'])),
});
export type ChildCollectQuery = v.InferOutput<typeof ChildCollectQuerySchema>;

/**
 * POST /api/sessions/:id/children/:child/collect: a child's report, what it changed and what it cost, at most 8 KiB in all.
 * `summary` is the last assistant text of the child's last turn, cut to 6 KiB (`summaryTruncated`). `usage` is
 * the child's own, estimated from the price table. With `release` (the default) the core stopped the child's
 * process; the session and its worktree stay. `released` says it did.
 */
export const ChildCollectSchema = v.object({
  sessionId: SessionIdSchema,
  state: ChildStateSchema,
  summary: v.string(),
  summaryTruncated: v.boolean(),
  changes: ChildChangesSchema,
  usage: v.object({ estimated: v.literal(true), ...UsageTotalsSchema.entries }),
  released: v.boolean(),
});
export type ChildCollect = v.InferOutput<typeof ChildCollectSchema>;

/** GET /api/sessions/:id/children/:child/diff?path=&cursor=&limit= — the page of one file's diff against the child's base. */
export const ChildDiffQuerySchema = v.object({
  path: TrimmedSchema,
  cursor: v.optional(v.pipe(v.string(), v.regex(/^\d{1,15}$/), v.transform(Number))),
  limit: v.optional(
    v.pipe(
      v.string(),
      v.regex(/^\d{1,6}$/),
      v.transform(Number),
      v.minValue(1),
      v.maxValue(MAX_CHILD_DIFF_BYTES),
    ),
  ),
});
export type ChildDiffQuery = v.InferOutput<typeof ChildDiffQuerySchema>;

export const ChildCancelQuerySchema = v.object({
  subtree: v.optional(v.picklist(['true', 'false'])),
});
export type ChildCancelQuery = v.InferOutput<typeof ChildCancelQuerySchema>;

/** The answer of POST /api/sessions/:id/children/:child/cancel?subtree=: the sessions that were interrupted and stopped, the child first. */
export const ChildCancelSchema = v.object({
  ok: v.literal(true),
  cancelled: v.array(SessionIdSchema),
});
export type ChildCancel = v.InferOutput<typeof ChildCancelSchema>;
