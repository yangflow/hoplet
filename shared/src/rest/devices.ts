import * as v from 'valibot';
import { DeviceProjectSchema, DirEntrySchema } from '../catalog.ts';
import { CloneCheckReasonSchema, CloneDestinationDetailSchema } from '../clone.ts';
import { AttachmentIdSchema, SessionIdSchema } from '../ids.ts';
import { ProviderInfoSchema } from '../runtime.ts';
import { DeviceSystemSchema } from '../wire/index.ts';
import { TrimmedSchema } from './common.ts';

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
  /** The host dev server links use for this machine, as its agent announced it; absent while offline. */
  previewHost: v.optional(v.string()),
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
