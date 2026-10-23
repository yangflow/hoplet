import * as v from 'valibot';
import { SessionIdSchema } from '../ids.ts';
import { TrimmedSchema } from './common.ts';

/** A changed path's git status against `HEAD` (or, in a snapshot, against its base commit). */
export const ChangeStatusSchema = v.picklist(['modified', 'added', 'deleted', 'untracked']);
export type ChangeStatus = v.InferOutput<typeof ChangeStatusSchema>;

/** One changed path: its status and its added and removed line counts, or `binary`. */
export const WorkspaceChangeSchema = v.object({
  path: v.string(),
  status: ChangeStatusSchema,
  added: v.optional(v.number()),
  removed: v.optional(v.number()),
  binary: v.optional(v.boolean()),
});
export type WorkspaceChange = v.InferOutput<typeof WorkspaceChangeSchema>;

/**
 * GET /api/sessions/:id/workspace: the owner's working tree, tracked and untracked, not ignored, at most 400
 * paths (`truncated` beyond), and every changed path. `git` is false for a directory without a commit; it
 * then has no paths and no changes. `origin` is the URL of the repository's remote `origin`, empty without one.
 */
export const WorkspaceListingSchema = v.object({
  sessionId: SessionIdSchema,
  git: v.boolean(),
  commit: v.string(),
  origin: v.string(),
  paths: v.array(v.string()),
  truncated: v.boolean(),
  changes: v.array(WorkspaceChangeSchema),
});
export type WorkspaceListing = v.InferOutput<typeof WorkspaceListingSchema>;

/** GET /api/sessions/:id/workspace/file?path=&at= — `at=head` reads the file as `HEAD` has it. */
export const WorkspaceFileQuerySchema = v.object({
  path: TrimmedSchema,
  at: v.optional(v.picklist(['worktree', 'head'])),
});
export type WorkspaceFileQuery = v.InferOutput<typeof WorkspaceFileQuerySchema>;

/**
 * One regular file inside the working tree (ADR 0010): `text` is at most its first 256 KiB (`truncated`
 * beyond); an image (PNG, JPEG, GIF, WebP, SVG) of at most 4 MiB comes whole as base64 `data` with its
 * `mediaType`; any other binary file comes as its `size` only.
 */
export const WorkspaceFileSchema = v.variant('kind', [
  v.object({
    kind: v.literal('text'),
    sessionId: SessionIdSchema,
    path: v.string(),
    size: v.number(),
    text: v.string(),
    truncated: v.boolean(),
  }),
  v.object({
    kind: v.literal('image'),
    sessionId: SessionIdSchema,
    path: v.string(),
    size: v.number(),
    mediaType: v.picklist(['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/svg+xml']),
    data: v.string(),
  }),
  v.object({
    kind: v.literal('binary'),
    sessionId: SessionIdSchema,
    path: v.string(),
    size: v.number(),
  }),
]);
export type WorkspaceFile = v.InferOutput<typeof WorkspaceFileSchema>;

/** GET /api/sessions/:id/snapshot/diff?path= */
export const SnapshotDiffQuerySchema = v.object({ path: TrimmedSchema });

/**
 * GET /api/sessions/:id/workspace/diff?path=&against=&cursor=&limit= — `against=base` compares with the commit
 * the session's worktree started from instead of `HEAD`. `cursor` and `limit` page the hunks: `cursor` is the
 * `next` of the previous page (absent for the first), `limit` the most bytes of hunks per page (1 to 262144;
 * 262144 when absent).
 */
export const WorkspaceDiffQuerySchema = v.object({
  path: TrimmedSchema,
  against: v.optional(v.picklist(['head', 'base'])),
  cursor: v.optional(v.pipe(v.string(), v.regex(/^\d{1,15}$/), v.transform(Number))),
  limit: v.optional(
    v.pipe(
      v.string(),
      v.regex(/^\d{1,6}$/),
      v.transform(Number),
      v.minValue(1),
      v.maxValue(256 * 1024),
    ),
  ),
});
export type WorkspaceDiffQuery = v.InferOutput<typeof WorkspaceDiffQuerySchema>;

/**
 * The unified diff of one changed path (`git diff` format, hunks only, no header lines), one page of at most
 * 256 KiB cut at a line end; an untracked file is all added lines. `size` is the whole length of the hunks and
 * `truncated` says more follows, with `next` the `cursor` of the following page. A binary change carries the
 * old and new sizes instead (absent on the side where the file does not exist).
 */
export const WorkspaceDiffSchema = v.variant('kind', [
  v.object({
    kind: v.literal('text'),
    sessionId: SessionIdSchema,
    path: v.string(),
    status: ChangeStatusSchema,
    diff: v.string(),
    truncated: v.boolean(),
    next: v.optional(v.number()),
    size: v.number(),
  }),
  v.object({
    kind: v.literal('binary'),
    sessionId: SessionIdSchema,
    path: v.string(),
    status: ChangeStatusSchema,
    oldSize: v.optional(v.number()),
    newSize: v.optional(v.number()),
  }),
]);
export type WorkspaceDiff = v.InferOutput<typeof WorkspaceDiffSchema>;

/**
 * GET /api/sessions/:id/workspace/changes — the files the session changed, committed or not, without content.
 * They differ from `base`: the commit the session's worktree started from, or `HEAD` when the session shares its
 * directory (`shared`; the changes are then everything uncommitted there, not the session's alone). `branch` is
 * empty on a detached `HEAD`. `files` lists at most 200 paths by path and `total` counts them all.
 */
export const WorkspaceChangesSchema = v.object({
  sessionId: SessionIdSchema,
  branch: v.string(),
  base: v.string(),
  shared: v.boolean(),
  total: v.number(),
  files: v.array(WorkspaceChangeSchema),
});
export type WorkspaceChanges = v.InferOutput<typeof WorkspaceChangesSchema>;

/** What a snapshot answer adds: the snapshot's time (ms since the epoch) and its base commit. */
const SnapshotBase = { snapshotAt: v.number(), baseCommit: v.string() };

/**
 * GET /api/sessions/:id/snapshot/changes — from the latest stored snapshot, owner online or not, with the URL
 * of its remote `origin` (empty without one) and the size of its compressed archive.
 */
export const SnapshotChangesSchema = v.object({
  sessionId: SessionIdSchema,
  ...SnapshotBase,
  origin: v.string(),
  byteSize: v.number(),
  changes: v.array(WorkspaceChangeSchema),
});
export type SnapshotChanges = v.InferOutput<typeof SnapshotChangesSchema>;

/** GET /api/sessions/:id/snapshot/diff?path= — one path's diff from the latest stored snapshot. */
export const SnapshotDiffSchema = v.intersect([WorkspaceDiffSchema, v.object(SnapshotBase)]);
export type SnapshotDiff = v.InferOutput<typeof SnapshotDiffSchema>;
