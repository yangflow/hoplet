/**
 * Deleting a session (ADR 0023): the pieces the agent reports and the REST answers share. The vendor file and
 * the worktree are described by the owner agent; the core adds the owner, the running check and the step list.
 */
import * as v from 'valibot';

/** The steps of a deletion in the order they run. `core-copy` is the core's own and always last. */
export const DELETION_STEPS = [
  'stop-syncing',
  'worktree',
  'branch',
  'vendor-file',
  'core-copy',
] as const;
export const DeletionStepNameSchema = v.picklist(DELETION_STEPS);
export type DeletionStepName = v.InferOutput<typeof DeletionStepNameSchema>;

/** `skipped` is a step not asked for, or one that did not run because an earlier step failed. */
export const DeletionStatusSchema = v.picklist(['done', 'failed', 'skipped']);
export type DeletionStatus = v.InferOutput<typeof DeletionStatusSchema>;

/** One step of a deletion. `reason` is diagnostic text of a failed step, never copy. */
export const DeletionStepSchema = v.object({
  step: DeletionStepNameSchema,
  status: DeletionStatusSchema,
  reason: v.optional(v.string()),
});
export type DeletionStep = v.InferOutput<typeof DeletionStepSchema>;

/** The vendor's session file on the owner. */
export const DeletionFileSchema = v.object({ path: v.string() });
export type DeletionFile = v.InferOutput<typeof DeletionFileSchema>;

/** Uncommitted work in a worktree: tracked files changed, and files git does not track. */
export const DeletionDirtySchema = v.object({ modified: v.number(), untracked: v.number() });
export type DeletionDirty = v.InferOutput<typeof DeletionDirtySchema>;

/** The worktree and branch a handoff made, as the agent finds them. `unpushed` counts commits no remote has. */
export const DeletionWorktreeSchema = v.object({
  path: v.string(),
  branch: v.string(),
  dirty: DeletionDirtySchema,
  unpushed: v.number(),
});
export type DeletionWorktree = v.InferOutput<typeof DeletionWorktreeSchema>;
