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

/** GET /api/sessions/:id/workspace/diff?path= and GET /api/sessions/:id/snapshot/diff?path= */
export const WorkspaceDiffQuerySchema = v.object({ path: TrimmedSchema });

/**
 * The unified diff of one changed path (`git diff` format, hunks only, no header lines), at most 256 KiB with
 * `truncated` and the full `size` beyond; an untracked file is all added lines. A binary change carries the
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
