import * as v from 'valibot';
import { ProviderSchema } from './session.ts';

/**
 * What a runtime driver declares it can do; the UI shows only the controls a session's driver supports.
 * `fast` and `thinking` are start options of a new session; `models` lists the model IDs a new session can
 * choose from and is empty when the driver cannot choose; `images` says a prompt may carry images.
 */
export const RuntimeCapabilitiesSchema = v.object({
  permissions: v.boolean(),
  interrupt: v.boolean(),
  thinking: v.boolean(),
  fast: v.boolean(),
  models: v.array(v.string()),
  images: v.optional(v.boolean()),
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

/**
 * Images that go with a prompt (docs/spec.md#hoplet, ADR 0017): at most 4, each at most 4 MiB and together at
 * most 8 MiB, of these types. The agent holds the same numbers in agent/internal/daemon/images.go.
 */
export const PROMPT_IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'] as const;
export type PromptImageType = (typeof PROMPT_IMAGE_TYPES)[number];
export const PROMPT_IMAGES_MAX = 4;
export const PROMPT_IMAGE_MAX_BYTES = 4 * 1024 * 1024;
export const PROMPT_IMAGES_MAX_BYTES = 8 * 1024 * 1024;

/** One image of a prompt: its media type and its bytes as base64. */
export const PromptImageSchema = v.object({
  mediaType: v.picklist(PROMPT_IMAGE_TYPES),
  data: v.pipe(v.string(), v.maxLength(Math.ceil(PROMPT_IMAGE_MAX_BYTES / 3) * 4)),
});
export type PromptImage = v.InferOutput<typeof PromptImageSchema>;

/** The images of a prompt, at most `PROMPT_IMAGES_MAX`. */
export const PromptImagesSchema = v.pipe(
  v.array(PromptImageSchema),
  v.maxLength(PROMPT_IMAGES_MAX),
);

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
