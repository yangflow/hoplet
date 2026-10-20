import * as v from 'valibot';
import { AttachmentIdSchema } from './ids.ts';
import { ProviderSchema } from './session.ts';

/**
 * The shared icon vocabulary of config values (docs/design.md#icons): semantic names, not assets. Each client maps
 * a name to an icon of its own platform and falls back to the generic icon of the option's category when a value
 * names none or a name the client does not know.
 */
export const CONFIG_ICONS = [
  'shield',
  'shield-alert',
  'file-pen',
  'list-checks',
  'sparkles',
] as const;
export const ConfigIconSchema = v.picklist(CONFIG_ICONS);
export type ConfigIcon = v.InferOutput<typeof ConfigIconSchema>;

/** One value of a `select` option: what a session's setting is set to, with the name a client shows. */
export const ConfigValueSchema = v.object({
  value: v.pipe(v.string(), v.minLength(1), v.maxLength(128)),
  name: v.string(),
  description: v.optional(v.string()),
  /** `high` marks a value with a high risk (ADR 0018); clients mark it. */
  risk: v.optional(v.picklist(['high'])),
  /** A name of the shared icon vocabulary; a client without it for a value draws its category's generic icon. */
  icon: v.optional(ConfigIconSchema),
});
export type ConfigValue = v.InferOutput<typeof ConfigValueSchema>;

const configOptionFields = {
  id: v.pipe(v.string(), v.minLength(1), v.maxLength(64)),
  name: v.string(),
  description: v.optional(v.string()),
  /** Free text; ACP reserves `mode`, `model`, `model_config` and `thought_level`. */
  category: v.optional(v.string()),
};

/**
 * A setting of a session as its runtime driver declares it (ADR 0018), shaped as an ACP session config option.
 * In a provider's capabilities `currentValue` is the default of a new session; in a session's runtime state it
 * is that session's value. The list's order is the driver's priority.
 */
export const ConfigOptionSchema = v.variant('type', [
  v.object({
    type: v.literal('select'),
    ...configOptionFields,
    options: v.array(ConfigValueSchema),
    currentValue: v.string(),
  }),
  v.object({
    type: v.literal('boolean'),
    ...configOptionFields,
    currentValue: v.boolean(),
  }),
]);
export type ConfigOption = v.InferOutput<typeof ConfigOptionSchema>;

/** The value of one option: a value of a `select`, or the state of a `boolean`. */
export const ConfigSettingSchema = v.union([ConfigValueSchema.entries.value, v.boolean()]);
export type ConfigSetting = v.InferOutput<typeof ConfigSettingSchema>;

/** The values a new session starts with, by option ID; an option left out keeps its default. */
export const SessionConfigSchema = v.record(configOptionFields.id, ConfigSettingSchema);
export type SessionConfig = v.InferOutput<typeof SessionConfigSchema>;

/** One change of a running session's setting. */
export const ConfigChangeSchema = v.object({
  configId: configOptionFields.id,
  value: ConfigSettingSchema,
});
export type ConfigChange = v.InferOutput<typeof ConfigChangeSchema>;

/**
 * What a runtime driver declares it can do; the UI shows only the controls a session's driver supports.
 * `permissions` and `interrupt` are the controls of a turn, `images` says a prompt may carry images, `steer`
 * says a message written during a turn reaches the model at the turn's next tool gap (ADR 0020), and
 * `config` lists the settings of a session (ADR 0018).
 */
export const RuntimeCapabilitiesSchema = v.object({
  permissions: v.boolean(),
  interrupt: v.boolean(),
  images: v.optional(v.boolean()),
  steer: v.optional(v.boolean()),
  config: v.array(ConfigOptionSchema),
});
export type RuntimeCapabilities = v.InferOutput<typeof RuntimeCapabilitiesSchema>;

/** The ID of a provider configuration: a slug the owner picks on the machine; `claude` and `codex` are the built-ins. */
export const ConfigurationIdSchema = v.pipe(v.string(), v.regex(/^[a-z0-9][a-z0-9-]{0,39}$/));
export type ConfigurationId = v.InferOutput<typeof ConfigurationIdSchema>;

/**
 * A provider configuration as the machine's providers.json says it (ADR 0028), without a single value of its
 * environment: `env` names the variables and says whether each is set. `command` and `env` are set on the
 * machine only; the core may change `name`, `enabled` and `defaults`.
 */
export const ProviderEnvVarSchema = v.object({
  name: v.pipe(v.string(), v.minLength(1), v.maxLength(128)),
  set: v.boolean(),
});
export type ProviderEnvVar = v.InferOutput<typeof ProviderEnvVarSchema>;

export const ProviderConfigurationSchema = v.object({
  id: ConfigurationIdSchema,
  name: v.pipe(v.string(), v.minLength(1), v.maxLength(64)),
  extends: ProviderSchema,
  enabled: v.boolean(),
  command: v.optional(v.pipe(v.string(), v.maxLength(4096))),
  env: v.array(ProviderEnvVarSchema),
  /** The default of a new session's options by option ID, over the driver's own defaults. */
  defaults: SessionConfigSchema,
});
export type ProviderConfiguration = v.InferOutput<typeof ProviderConfigurationSchema>;

/**
 * What an agent reports for a provider configuration it runs: the capabilities of its runtime driver and the
 * installed vendor `version`; `provider` is the vendor it extends. `tested` is true when the driver was verified against exactly that version and
 * false when the version is newer than the tested ones; both are absent when the version cannot be read or
 * is refused.
 */
export const ProviderInfoSchema = v.object({
  provider: ProviderSchema,
  configuration: ProviderConfigurationSchema,
  capabilities: RuntimeCapabilitiesSchema,
  version: v.optional(v.string()),
  tested: v.optional(v.boolean()),
});
export type ProviderInfo = v.InferOutput<typeof ProviderInfoSchema>;

/**
 * The entry of `providers` for `provider` under `configuration`; the vendor's own configuration (whose ID is the
 * vendor's name) when `configuration` is absent.
 */
export function findProvider(
  providers: readonly ProviderInfo[],
  provider: ProviderInfo['provider'],
  configuration?: string,
): ProviderInfo | undefined {
  const id = configuration ?? provider;
  return providers.find((p) => p.provider === provider && p.configuration.id === id);
}

/** Whether `value` is a setting `option` accepts: a declared value of a `select`, a boolean for a `boolean`. */
function accepts(option: ConfigOption, value: ConfigSetting): boolean {
  if (option.type === 'boolean') return typeof value === 'boolean';
  return typeof value === 'string' && option.options.some((o) => o.value === value);
}

/**
 * The ID of the first setting of `values` that `caps` does not declare, or null: an unknown option, a value
 * of the wrong type or a `select` value outside its options. A new session's config and a change are checked
 * alike (pass a change as `{ [configId]: value }`).
 */
export function undeclaredConfig(
  values: Record<string, ConfigSetting>,
  caps: RuntimeCapabilities,
): string | null {
  for (const [id, value] of Object.entries(values)) {
    const option = caps.config.find((o) => o.id === id);
    if (!option || !accepts(option, value)) return id;
  }
  return null;
}

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

/** The longest feedback a refusal may carry to the model. */
export const PERMISSION_MESSAGE_MAX = 10_000;

/**
 * A driver's declaration that a permission request asks to leave a plan (ADR 0019): the plan to review as
 * Markdown, the file it lives in when the provider keeps one, and the values of its select setting `option`
 * that approving may switch the session to, `default` being the one offered first. A request without it is an
 * ordinary tool permission. `freshContext` is true when the provider can start a new session from the approved
 * plan (ADR 0025).
 */
export const PlanExitSchema = v.object({
  plan: v.string(),
  planFile: v.optional(v.string()),
  option: v.string(),
  modes: v.pipe(v.array(v.string()), v.minLength(1)),
  default: v.string(),
  freshContext: v.optional(v.boolean()),
});
export type PlanExit = v.InferOutput<typeof PlanExitSchema>;

/**
 * Images that go with a prompt (docs/spec.md#hoplet, ADR 0017): uploaded one by one to the machine first, then
 * referred to by ID. Each is PNG, JPEG, GIF or WebP of at most 4 MiB; a prompt takes at most 4 and together at
 * most 8 MiB. The agent holds the same numbers in agent/internal/daemon/attachments.go.
 */
export const PROMPT_IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'] as const;
export type PromptImageType = (typeof PROMPT_IMAGE_TYPES)[number];
export const PROMPT_IMAGES_MAX = 4;
export const PROMPT_IMAGE_MAX_BYTES = 4 * 1024 * 1024;
export const PROMPT_IMAGES_MAX_BYTES = 8 * 1024 * 1024;

/** One image to upload: its media type and its bytes as base64. */
export const PromptImageSchema = v.object({
  mediaType: v.picklist(PROMPT_IMAGE_TYPES),
  data: v.pipe(v.string(), v.maxLength(Math.ceil(PROMPT_IMAGE_MAX_BYTES / 3) * 4)),
});
export type PromptImage = v.InferOutput<typeof PromptImageSchema>;

/**
 * The image type the first bytes of a file show, or null: PNG, JPEG, GIF and WebP by the signatures Go's
 * `http.DetectContentType` uses, so the core and the agent agree.
 */
export function sniffImageType(head: Uint8Array): PromptImageType | null {
  const at = (i: number, bytes: number[]) => bytes.every((b, k) => head[i + k] === b);
  if (at(0, [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])) return 'image/png';
  if (at(0, [0xff, 0xd8, 0xff])) return 'image/jpeg';
  if (at(0, [0x47, 0x49, 0x46, 0x38]) && (at(4, [0x37, 0x61]) || at(4, [0x39, 0x61])))
    return 'image/gif';
  if (at(0, [0x52, 0x49, 0x46, 0x46]) && at(8, [0x57, 0x45, 0x42, 0x50, 0x56, 0x50]))
    return 'image/webp';
  return null;
}

/** The images of a prompt, by the IDs their upload returned: at most `PROMPT_IMAGES_MAX`. */
export const PromptAttachmentsSchema = v.pipe(
  v.array(AttachmentIdSchema),
  v.maxLength(PROMPT_IMAGES_MAX),
);
