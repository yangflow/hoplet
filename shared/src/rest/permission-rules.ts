import * as v from 'valibot';
import { SessionIdSchema } from '../ids.ts';
import {
  LocalPermissionRuleSchema,
  PermissionActionSchema,
  PermissionMatchSchema,
  PermissionNoteSchema,
  PermissionRuleSchema,
  PermissionScopeSchema,
  PermissionToolSchema,
} from '../permission-rules.ts';
import { TrimmedSchema } from './common.ts';

/** A rule ID: the opaque ID of a rule the user made, or `default.<name>` of a built-in. */
export const PermissionRuleIdSchema = v.pipe(
  v.string(),
  v.regex(/^([A-Za-z0-9_-]{1,64}|default\.[a-z][a-z-]*)$/),
);

const ProjectPathSchema = v.pipe(TrimmedSchema, v.maxLength(4096));

/** Where a rule applies besides its `scope`: `project` has a machine and a path, `session` a session ID. */
const Targets = {
  deviceId: v.optional(TrimmedSchema),
  projectPath: v.optional(ProjectPathSchema),
  sessionId: v.optional(SessionIdSchema),
};

/** POST and PUT /api/permission-rules: the whole definition of a rule the user makes. The route checks the targets and the matcher. */
export const PermissionRuleBodySchema = v.strictObject({
  scope: PermissionScopeSchema,
  ...Targets,
  tool: PermissionToolSchema,
  match: PermissionMatchSchema,
  action: PermissionActionSchema,
  note: v.optional(PermissionNoteSchema, ''),
  enabled: v.optional(v.boolean(), true),
});
export type PermissionRuleBody = v.InferOutput<typeof PermissionRuleBodySchema>;

/** PUT /api/permission-rules/:id of a built-in: only the switch. Turning one off needs `confirm` equal to its ID. */
export const PermissionBuiltinToggleSchema = v.strictObject({
  enabled: v.boolean(),
  confirm: v.optional(v.string()),
});
export type PermissionBuiltinToggle = v.InferOutput<typeof PermissionBuiltinToggleSchema>;

export const PermissionRulePutSchema = v.union([
  PermissionRuleBodySchema,
  PermissionBuiltinToggleSchema,
]);
export type PermissionRulePut = v.InferOutput<typeof PermissionRulePutSchema>;

/** The rule as the agent reads it, where it applies, and when it changed (built-ins have no times). */
export const PermissionRuleEntrySchema = v.strictObject({
  ...PermissionRuleSchema.entries,
  ...Targets,
  createdAt: v.optional(v.number()),
  updatedAt: v.optional(v.number()),
});
export type PermissionRuleEntry = v.InferOutput<typeof PermissionRuleEntrySchema>;

/** GET /api/permission-rules. `deviceId`, `projectPath` and `sessionId` narrow the rules of their scope. */
export const PermissionRuleListQuerySchema = v.strictObject({
  scope: v.optional(PermissionScopeSchema),
  deviceId: v.optional(TrimmedSchema),
  projectPath: v.optional(ProjectPathSchema),
  sessionId: v.optional(SessionIdSchema),
});
export type PermissionRuleListQuery = v.InferOutput<typeof PermissionRuleListQuerySchema>;

/** The deny rules one machine's agent announced from its own configuration; read-only. */
export const LocalPermissionRulesSchema = v.strictObject({
  deviceId: v.string(),
  rules: v.array(LocalPermissionRuleSchema),
});
export type LocalPermissionRules = v.InferOutput<typeof LocalPermissionRulesSchema>;

/** The built-ins first, then the rules the user made in the order they were made. */
export const PermissionRuleListResponseSchema = v.object({
  rules: v.array(PermissionRuleEntrySchema),
  /** The revision of the rules that apply to the machine named by `deviceId` (or owning `sessionId`); absent when neither is given. */
  revision: v.optional(v.number()),
  /**
   * The deny rules the agents announced from their own configuration (`permission_deny`), one entry per machine that
   * has any; only the machine named by `deviceId` (or owning `sessionId`) when one is given. Read-only. GET only.
   */
  local: v.optional(v.array(LocalPermissionRulesSchema)),
});
export type PermissionRuleListResponse = v.InferOutput<typeof PermissionRuleListResponseSchema>;

/**
 * Where the rules stand on one machine (GET /api/permission-rules/machines). `revision` is the revision the
 * machine should be at; `applied` the one it last confirmed and `appliedAt` when, `pushedAt` when the core last
 * sent it the rules. All three are null until known: the core keeps them in memory only. `retrying` says the core
 * will send the rules again on its own because no confirmation came. `lastSeen` is the agent's last connection
 * while it is offline.
 */
export const PermissionRuleMachineSchema = v.object({
  deviceId: v.string(),
  name: v.string(),
  online: v.boolean(),
  revision: v.number(),
  applied: v.nullable(v.number()),
  appliedAt: v.nullable(v.number()),
  pushedAt: v.nullable(v.number()),
  retrying: v.boolean(),
  lastSeen: v.optional(v.number()),
});
export type PermissionRuleMachine = v.InferOutput<typeof PermissionRuleMachineSchema>;

/** `latest` is the highest `revision` among the machines (0 with none). Machines that never connected are not listed. */
export const PermissionRuleMachinesResponseSchema = v.object({
  latest: v.number(),
  machines: v.array(PermissionRuleMachineSchema),
});
export type PermissionRuleMachinesResponse = v.InferOutput<
  typeof PermissionRuleMachinesResponseSchema
>;
