import * as v from 'valibot';
import { CommandSchema, CommandsOriginSchema } from '../commands.ts';
import { PERMISSION_MESSAGE_MAX, PermissionDecisionSchema } from '../runtime.ts';
import { TrimmedSchema } from './common.ts';

/** POST /api/sessions/:id/permission */
export const PermissionRequestSchema = v.object({
  requestId: TrimmedSchema,
  decision: PermissionDecisionSchema,
  /** A refusal's reason, told to the model: "Keep planning" sends the user's feedback here. */
  message: v.optional(v.pipe(v.string(), v.maxLength(PERMISSION_MESSAGE_MAX))),
  /** The value of the plan request's option that an approval switches the session to. */
  mode: v.optional(v.pipe(v.string(), v.minLength(1), v.maxLength(64))),
});
export type PermissionRequest = v.InferOutput<typeof PermissionRequestSchema>;

/** The answer of /permission, /interrupt and /stop: the command was forwarded to the owner. */
export const RuntimeCommandResultSchema = v.object({ ok: v.literal(true) });
export type RuntimeCommandResult = v.InferOutput<typeof RuntimeCommandResultSchema>;

/**
 * GET /api/sessions/:id/commands and GET /api/devices/:id/commands?cwd=: the commands and skills a prompt may
 * invoke as `/name args`, commands first and each group by name. `source` says where the list came from: the
 * session's own (`session`), the last one a session in that directory reported (`directory`), or the built-ins
 * Hoplet offers (`builtin`), which is what a directory without a reported list gets.
 */
export const CommandsResultSchema = v.object({
  commands: v.array(CommandSchema),
  source: CommandsOriginSchema,
});
export type CommandsResult = v.InferOutput<typeof CommandsResultSchema>;

/** GET /api/devices/:id/commands?cwd= — the directory a draft session would start in. */
export const DeviceCommandsQuerySchema = v.object({ cwd: TrimmedSchema });
export type DeviceCommandsQuery = v.InferOutput<typeof DeviceCommandsQuerySchema>;
