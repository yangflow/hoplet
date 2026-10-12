import * as v from 'valibot';
import { DeviceProjectSchema, DirEntrySchema } from '../catalog.ts';
import { SessionIdSchema } from '../ids.ts';
import { ProviderInfoSchema } from '../runtime.ts';
import { TrimmedSchema } from './common.ts';

/** GET /api/devices */
export const DeviceInfoSchema = v.object({
  deviceId: v.string(),
  name: v.string(),
  connected: v.boolean(),
  /** The providers the connected agent runs, with their capabilities; empty while the device is offline. */
  providers: v.array(ProviderInfoSchema),
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
});
export type DeviceCloneQuery = v.InferOutput<typeof DeviceCloneQuerySchema>;

export const DeviceCloneResultSchema = v.object({
  deviceId: v.string(),
  path: v.nullable(v.string()),
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
