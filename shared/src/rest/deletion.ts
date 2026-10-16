import * as v from 'valibot';
import { DeletionFileSchema, DeletionStepSchema, DeletionWorktreeSchema } from '../deletion.ts';

/** GET /api/sessions/:id/deletion: what deleting the session would remove. */
export const DeletionPreviewSchema = v.object({
  owner: v.object({ deviceId: v.string(), name: v.string(), online: v.boolean() }),
  /** A Hoplet-managed process is alive; the session cannot be deleted until it is stopped. */
  running: v.boolean(),
  /** The vendor file on the owner; `null` when the owner is offline or has none. */
  vendorFile: v.nullable(DeletionFileSchema),
  /** The worktree and branch a handoff made; `null` when the owner is offline, none was recorded or it is gone. */
  worktree: v.nullable(DeletionWorktreeSchema),
  /** The device that owned the session before its last handoff; its file and worktree stay. `null` when unknown. */
  previousOwner: v.nullable(v.object({ deviceId: v.string(), name: v.string() })),
});
export type DeletionPreview = v.InferOutput<typeof DeletionPreviewSchema>;

/** DELETE /api/sessions/:id: the core copy is always deleted, the rest only when asked for. */
export const DeleteSessionRequestSchema = v.object({
  vendorFile: v.boolean(),
  worktree: v.boolean(),
  /** True only when the preview showed the worktree dirty and the user ticked it anyway. */
  worktreeDirty: v.boolean(),
});
export type DeleteSessionRequest = v.InferOutput<typeof DeleteSessionRequestSchema>;

/** The answer of a deletion that finished: every step, the last being `core-copy`. */
export const DeleteSessionResponseSchema = v.object({ steps: v.array(DeletionStepSchema) });
export type DeleteSessionResponse = v.InferOutput<typeof DeleteSessionResponseSchema>;
