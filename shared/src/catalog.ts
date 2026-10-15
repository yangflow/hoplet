import * as v from 'valibot';
import { SessionIdSchema } from './ids.ts';

/** A session found in a watched project folder on a device. */
export const DeviceProjectSessionSchema = v.object({
  sessionId: SessionIdSchema,
  cwd: v.string(),
  imported: v.boolean(),
  /** One-line title from the first user prompt when the agent can read it. */
  title: v.optional(v.string()),
});

/** A watched project: its path, the git branch checked out there (absent outside a repository), its sessions. */
export const DeviceProjectSchema = v.object({
  path: v.string(),
  branch: v.optional(v.string()),
  sessions: v.array(DeviceProjectSessionSchema),
});

/** A directory under the agent's browse root; `git` when it holds a `.git` entry. */
export const DirEntrySchema = v.object({ name: v.string(), git: v.boolean() });
export type DirEntry = v.InferOutput<typeof DirEntrySchema>;

export type DeviceProjectSession = v.InferOutput<typeof DeviceProjectSessionSchema>;
export type DeviceProject = v.InferOutput<typeof DeviceProjectSchema>;
