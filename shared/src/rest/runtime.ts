import * as v from 'valibot';
import { PermissionDecisionSchema } from '../runtime.ts';
import { TrimmedSchema } from './common.ts';

/** POST /api/sessions/:id/permission */
export const PermissionRequestSchema = v.object({
  requestId: TrimmedSchema,
  decision: PermissionDecisionSchema,
});
export type PermissionRequest = v.InferOutput<typeof PermissionRequestSchema>;

/** The answer of /permission, /interrupt and /stop: the command was forwarded to the owner. */
export const RuntimeCommandResultSchema = v.object({ ok: v.literal(true) });
export type RuntimeCommandResult = v.InferOutput<typeof RuntimeCommandResultSchema>;
