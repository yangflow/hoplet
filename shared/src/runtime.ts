import * as v from 'valibot';
import { ProviderSchema } from './session.ts';

/**
 * What a runtime driver declares it can do; the UI shows only the controls a session's driver supports.
 * `fast` and `thinking` are start options of a new session; `models` lists the model IDs a new session can
 * choose from and is empty when the driver cannot choose.
 */
export const RuntimeCapabilitiesSchema = v.object({
  permissions: v.boolean(),
  interrupt: v.boolean(),
  thinking: v.boolean(),
  fast: v.boolean(),
  models: v.array(v.string()),
});
export type RuntimeCapabilities = v.InferOutput<typeof RuntimeCapabilitiesSchema>;

/**
 * What an agent reports for a provider it runs: the capabilities of that provider's runtime driver and the
 * installed vendor `version`. `tested` is true when the driver was verified against exactly that version and
 * false when the version is newer than the tested ones; both are absent when the version cannot be read or
 * is refused.
 */
export const ProviderInfoSchema = v.object({
  provider: ProviderSchema,
  capabilities: RuntimeCapabilitiesSchema,
  version: v.optional(v.string()),
  tested: v.optional(v.boolean()),
});
export type ProviderInfo = v.InferOutput<typeof ProviderInfoSchema>;

/** The options of a new session; only what the provider's capabilities declare is accepted. */
export const StartOptionsSchema = v.object({
  model: v.optional(v.pipe(v.string(), v.minLength(1), v.maxLength(128))),
  thinking: v.optional(v.boolean()),
  fast: v.optional(v.boolean()),
});
export type StartOptions = v.InferOutput<typeof StartOptionsSchema>;

/** The runtime state of a session an agent reports (docs/spec.md, runtime states). */
export const AgentRuntimeStateSchema = v.picklist([
  'external',
  'idle',
  'starting',
  'ready',
  'running',
  'waiting-permission',
  'failed',
]);
export type AgentRuntimeState = v.InferOutput<typeof AgentRuntimeStateSchema>;

/** What browsers see: the agent's states plus `offline`, which the core adds when the owner is not connected. */
export const RuntimeStateSchema = v.picklist([
  'offline',
  'external',
  'idle',
  'starting',
  'ready',
  'running',
  'waiting-permission',
  'failed',
]);
export type RuntimeState = v.InferOutput<typeof RuntimeStateSchema>;

/** What streamed output carries. Runtime output is transient: the session file is the record. */
export const RuntimeOutputKindSchema = v.picklist(['text', 'thinking', 'tool-call', 'tool-result']);
export type RuntimeOutputKind = v.InferOutput<typeof RuntimeOutputKindSchema>;

export const PermissionDecisionSchema = v.picklist(['allow', 'deny']);
export type PermissionDecision = v.InferOutput<typeof PermissionDecisionSchema>;
