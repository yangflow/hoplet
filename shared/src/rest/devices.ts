import * as v from 'valibot';
import { DeviceProjectSchema, DirEntrySchema } from '../catalog.ts';
import { CloneCheckReasonSchema, CloneDestinationDetailSchema } from '../clone.ts';
import { AttachmentIdSchema, SessionIdSchema } from '../ids.ts';
import {
  ConfigurationIdSchema,
  ProviderConfigurationSchema,
  ProviderInfoSchema,
  SessionConfigSchema,
} from '../runtime.ts';
import {
  DeviceSystemSchema,
  INVENTORY_GPUS_MAX,
  INVENTORY_REPOSITORIES_MAX,
  INVENTORY_TOOLCHAINS_MAX,
  InventoryGpuSchema,
  InventoryRepositorySchema,
  InventoryToolchainSchema,
} from '../wire/index.ts';
import { TrimmedSchema } from './common.ts';

/** One reading of a machine: `cpu` is null while the agent had no earlier reading to compare with. */
export const DeviceResourceSampleSchema = v.object({
  /** When the agent took the reading, in epoch milliseconds. */
  at: v.number(),
  cpu: v.nullable(v.number()),
  memUsed: v.number(),
  memTotal: v.number(),
  diskFree: v.number(),
  diskTotal: v.number(),
});
export type DeviceResourceSample = v.InferOutput<typeof DeviceResourceSampleSchema>;

/** The most labels one machine carries. */
export const MAX_DEVICE_LABELS = 20;
/** The most characters of one label. */
export const DEVICE_LABEL_MAX = 32;

/** A label of a machine: lowercase letters, digits and hyphens, 1 to 32 characters, starting with a letter or digit. */
export const DeviceLabelSchema = v.pipe(
  v.string(),
  v.maxLength(DEVICE_LABEL_MAX),
  v.regex(/^[a-z0-9][a-z0-9-]*$/),
);

/**
 * What the machine's agent last detected on it, kept while the machine is offline: the tools found with their
 * versions, its GPUs, and the `origin` of each watched project. `at` is when the agent detected it, in epoch
 * milliseconds.
 */
export const DeviceInventorySchema = v.object({
  at: v.pipe(v.number(), v.integer(), v.minValue(0)),
  toolchains: v.pipe(v.array(InventoryToolchainSchema), v.maxLength(INVENTORY_TOOLCHAINS_MAX)),
  gpus: v.pipe(v.array(InventoryGpuSchema), v.maxLength(INVENTORY_GPUS_MAX)),
  repositories: v.pipe(v.array(InventoryRepositorySchema), v.maxLength(INVENTORY_REPOSITORIES_MAX)),
});
export type DeviceInventory = v.InferOutput<typeof DeviceInventorySchema>;

/**
 * PUT /api/devices/:deviceId/labels: replaces the machine's labels. Duplicates are dropped, keeping the first;
 * at most `MAX_DEVICE_LABELS`.
 */
export const DeviceLabelsRequestSchema = v.object({
  labels: v.pipe(v.array(DeviceLabelSchema), v.maxLength(MAX_DEVICE_LABELS)),
});
export type DeviceLabelsRequest = v.InferOutput<typeof DeviceLabelsRequestSchema>;

/** The answer: the machine's labels after the change. */
export const DeviceLabelsResultSchema = v.object({ labels: v.array(DeviceLabelSchema) });
export type DeviceLabelsResult = v.InferOutput<typeof DeviceLabelsResultSchema>;

/** GET /api/devices */
export const DeviceInfoSchema = v.object({
  deviceId: v.string(),
  name: v.string(),
  connected: v.boolean(),
  /** The system its agent last announced; null for a device whose agent never did. Kept while the device is offline. */
  system: v.nullable(DeviceSystemSchema),
  /** When the agent last connected or disconnected, in epoch milliseconds; absent for a device that never connected. */
  lastSeen: v.optional(v.number()),
  /** The providers the connected agent runs, with their capabilities; empty while the device is offline. */
  providers: v.array(ProviderInfoSchema),
  /** Present and true when the connected agent offers the restricted terminal (ADR 0026). */
  terminal: v.optional(v.boolean()),
  /** Present while the agent is connected: whether its owner enabled `session_mcp` (ADR 0034). */
  sessionMcp: v.optional(v.boolean()),
  /** The host dev server links use for this machine, as its agent announced it; absent while offline. */
  previewHost: v.optional(v.string()),
  /** The latest resource report of the machine, kept while it is offline; absent for a machine that never reported. */
  resources: v.optional(DeviceResourceSampleSchema),
  /** Whether memory or disk is short; true only while the machine is connected. Absent with `resources`. */
  warn: v.optional(v.object({ memory: v.boolean(), disk: v.boolean() })),
  /** What its agent last detected on the machine, kept while it is offline; absent for a machine that never reported. */
  inventory: v.optional(DeviceInventorySchema),
  /** The labels the host gave the machine; empty by default. */
  labels: v.array(DeviceLabelSchema),
});
export type DeviceInfo = v.InferOutput<typeof DeviceInfoSchema>;

export const DeviceListResponseSchema = v.object({ devices: v.array(DeviceInfoSchema) });
export type DeviceListResponse = v.InferOutput<typeof DeviceListResponseSchema>;

/** POST /api/devices */
export const DeviceMintRequestSchema = v.object({
  name: TrimmedSchema,
  deviceId: v.optional(TrimmedSchema),
});
export type DeviceMintRequest = v.InferOutput<typeof DeviceMintRequestSchema>;

export const DeviceMintResultSchema = v.object({
  deviceId: v.string(),
  token: v.string(),
  name: v.string(),
});
export type DeviceMintResult = v.InferOutput<typeof DeviceMintResultSchema>;

/** GET /api/devices/:id/projects and the result of every project and session mutation below */
export const DeviceProjectsResultSchema = v.object({
  deviceId: v.string(),
  candidates: v.array(v.string()),
  projects: v.array(DeviceProjectSchema),
});
export type DeviceProjectsResult = v.InferOutput<typeof DeviceProjectsResultSchema>;

/** POST /api/devices/:id/projects and POST /api/devices/:id/projects/remove */
export const DeviceProjectsAddRequestSchema = v.object({ path: TrimmedSchema });
export type DeviceProjectsAddRequest = v.InferOutput<typeof DeviceProjectsAddRequestSchema>;
export const DeviceProjectsRemoveRequestSchema = DeviceProjectsAddRequestSchema;
export type DeviceProjectsRemoveRequest = DeviceProjectsAddRequest;

/**
 * POST /api/devices/:id/providers: changes the name, the enabled flag or the defaults of one provider
 * configuration on the machine (ADR 0028); a field left out stays as it is. `defaults` replaces the configuration's
 * defaults as a whole and must stay within what the driver declares. The command and the environment are changed
 * on the machine only.
 */
export const DeviceProvidersUpdateRequestSchema = v.object({
  id: ConfigurationIdSchema,
  name: v.optional(ProviderConfigurationSchema.entries.name),
  enabled: v.optional(v.boolean()),
  defaults: v.optional(SessionConfigSchema),
});
export type DeviceProvidersUpdateRequest = v.InferOutput<typeof DeviceProvidersUpdateRequestSchema>;

/** The answer: the machine's provider configurations after the change. */
export const DeviceProvidersResultSchema = v.object({
  deviceId: v.string(),
  providers: v.array(ProviderInfoSchema),
});
export type DeviceProvidersResult = v.InferOutput<typeof DeviceProvidersResultSchema>;

/** POST /api/devices/:id/sessions/import and /remove (the same body: import marks to set or clear) */
export const DeviceSessionsImportRequestSchema = v.object({
  path: TrimmedSchema,
  sessionIds: v.pipe(v.array(SessionIdSchema), v.minLength(1)),
});
export type DeviceSessionsImportRequest = v.InferOutput<typeof DeviceSessionsImportRequestSchema>;
export const DeviceSessionsRemoveRequestSchema = DeviceSessionsImportRequestSchema;
export type DeviceSessionsRemoveRequest = DeviceSessionsImportRequest;

/**
 * GET /api/devices/:id/clone?origin= — the clone a handoff to this device would add its worktree to: the main
 * working tree of a watched project whose remote `origin` is `origin`, or null when there is none.
 */
export const DeviceCloneQuerySchema = v.object({
  origin: v.pipe(TrimmedSchema, v.maxLength(2048)),
  /** The destination to check when there is no clone; absent, the answer suggests one (ADR 0024). */
  cloneInto: v.optional(v.pipe(TrimmedSchema, v.maxLength(4096))),
});
export type DeviceCloneQuery = v.InferOutput<typeof DeviceCloneQuerySchema>;

/** What cloning on a device with no clone would do: `origin` has credentials removed. */
export const DeviceCloneOfferSchema = v.object({
  origin: v.string(),
  repoName: v.string(),
  cloneInto: v.string(),
  reason: CloneCheckReasonSchema,
  detail: v.optional(CloneDestinationDetailSchema),
});
export type DeviceCloneOffer = v.InferOutput<typeof DeviceCloneOfferSchema>;

export const DeviceCloneResultSchema = v.object({
  deviceId: v.string(),
  path: v.nullable(v.string()),
  /** Present only when `path` is null. */
  clone: v.optional(DeviceCloneOfferSchema),
});
export type DeviceCloneResult = v.InferOutput<typeof DeviceCloneResultSchema>;

/** GET /api/devices/:id/dirs?path= (absent or `~` is the browse root) */
export const DeviceDirsQuerySchema = v.object({ path: v.optional(TrimmedSchema) });
export type DeviceDirsQuery = v.InferOutput<typeof DeviceDirsQuerySchema>;

export const DeviceDirsResultSchema = v.object({
  /** The resolved absolute path. */
  path: v.string(),
  /** Null at the browse root. */
  parent: v.nullable(v.string()),
  root: v.string(),
  entries: v.array(DirEntrySchema),
  truncated: v.boolean(),
});
export type DeviceDirsResult = v.InferOutput<typeof DeviceDirsResultSchema>;

/**
 * POST /api/devices/:id/attachments: one image for a coming prompt (ADR 0017), the body a `PromptImage`. The
 * device keeps it until a prompt uses it or for an hour; the answer names it.
 */
export const AttachmentResultSchema = v.object({ attachmentId: AttachmentIdSchema });
export type AttachmentResult = v.InferOutput<typeof AttachmentResultSchema>;

/** One reading of a session's processes: when it was taken, the CPU percent (null when unknown) and resident bytes. */
export const DeviceSessionPointSchema = v.object({
  at: v.number(),
  cpu: v.nullable(v.number()),
  rss: v.number(),
});
export type DeviceSessionPoint = v.InferOutput<typeof DeviceSessionPointSchema>;

/** The processes of one session on a machine, summed over the process tree, as of the latest report. */
export const DeviceSessionResourcesSchema = v.object({
  sessionId: SessionIdSchema,
  /** The session's name in Hoplet; null for a session the store does not know. */
  title: v.nullable(v.string()),
  /** The session's project directory; null for a session the store does not know. */
  projectPath: v.nullable(v.string()),
  /** Percent of the whole machine's CPU; null while the agent had no earlier reading. */
  cpu: v.nullable(v.number()),
  /** Resident memory in bytes. */
  rss: v.number(),
  /** Descendant processes. */
  children: v.number(),
  /** The session's readings within the last hour, oldest first; the last one is the latest report. */
  history: v.array(DeviceSessionPointSchema),
});
export type DeviceSessionResources = v.InferOutput<typeof DeviceSessionResourcesSchema>;

/**
 * GET /api/devices/:id/resources — the last hour of the machine's readings, oldest first (empty for a machine that
 * never reported), and the sessions of the latest one, the largest in memory first. `at` is the latest reading's
 * time, absent without one.
 */
export const DeviceResourcesResultSchema = v.object({
  samples: v.array(DeviceResourceSampleSchema),
  sessions: v.array(DeviceSessionResourcesSchema),
  at: v.optional(v.number()),
});
export type DeviceResourcesResult = v.InferOutput<typeof DeviceResourcesResultSchema>;
