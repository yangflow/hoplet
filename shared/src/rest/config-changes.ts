import * as v from 'valibot';
import { TrimmedSchema } from './common.ts';

/**
 * Changes a session proposes to its own configuration (ADR 0036). The kinds are closed: the project's memory
 * file, a project skill, a project command, and the two session options `model` and `effort`. Nothing is
 * user-level. A request lists up to `MAX_CONFIG_CHANGE_OBJECTS` objects and is approved or declined as a whole.
 */
export const CONFIG_OBJECT_KINDS = ['memory', 'skill', 'command', 'config'] as const;
export const ConfigObjectKindSchema = v.picklist(CONFIG_OBJECT_KINDS);
export type ConfigObjectKind = v.InferOutput<typeof ConfigObjectKindSchema>;

/** The only name of a `memory` object: the project's `CLAUDE.md` or `AGENTS.md`, whichever the provider reads. */
export const CONFIG_MEMORY_NAME = 'project';
/** The only names of a `config` object. */
export const CONFIG_OPTION_NAMES = ['model', 'effort'] as const;
/** The name of a `skill` or a `command`: a directory or file name of the project. */
export const CONFIG_FILE_NAME_PATTERN = /^[a-z0-9][a-z0-9-]{0,63}$/;

export const MAX_CONFIG_CHANGE_OBJECTS = 5;
/** The most bytes of new content one request carries in all (and one stored previous content may hold). */
export const MAX_CONFIG_CHANGE_BYTES = 64 * 1024;
export const MAX_CONFIG_CHANGE_REASON_CHARS = 500;
/** How long a request nobody decided stays open. */
export const CONFIG_CHANGE_TTL_MS = 24 * 60 * 60_000;
/** How long the core keeps the previous content of an applied change for undo. */
export const CONFIG_CHANGE_PREVIOUS_RETENTION_MS = 30 * 24 * 60 * 60_000;

/** Whether `name` is a valid name for the kind. */
export function validConfigObjectName(kind: ConfigObjectKind, name: string): boolean {
  switch (kind) {
    case 'memory':
      return name === CONFIG_MEMORY_NAME;
    case 'config':
      return (CONFIG_OPTION_NAMES as readonly string[]).includes(name);
    default:
      return CONFIG_FILE_NAME_PATTERN.test(name);
  }
}

/** The key of an object in the audit log and the card: `memory:project`, `skill:review`, `config:model`. */
export function configObjectKey(kind: ConfigObjectKind, name: string): string {
  return `${kind}:${name}`;
}

export const CONFIG_CHANGE_STATES = [
  'pending',
  'approved',
  'applied',
  'declined',
  'expired',
  'failed',
  'undone',
] as const;
export const ConfigChangeStateSchema = v.picklist(CONFIG_CHANGE_STATES);
export type ConfigChangeState = v.InferOutput<typeof ConfigChangeStateSchema>;

/**
 * One object a session proposes to write. `baseHash` is the SHA-256 (hex) of the file the proposal was written
 * against, empty for a file that does not exist yet; a `config` object has no base. `content` null deletes the
 * file (`skill` and `command` only).
 */
export const ConfigChangeObjectInputSchema = v.pipe(
  v.strictObject({
    kind: ConfigObjectKindSchema,
    name: v.pipe(TrimmedSchema, v.maxLength(64)),
    baseHash: v.optional(v.pipe(v.string(), v.maxLength(64))),
    content: v.nullable(v.string()),
  }),
  v.check((o) => validConfigObjectName(o.kind, o.name), 'name does not fit the kind'),
  v.check((o) => o.content !== null || o.kind === 'skill' || o.kind === 'command', 'cannot delete'),
);
export type ConfigChangeObjectInput = v.InferOutput<typeof ConfigChangeObjectInputSchema>;

/** POST /api/sessions/:sessionId/config-changes, with the session's own token. */
export const SessionConfigChangeCreateSchema = v.pipe(
  v.strictObject({
    reason: v.pipe(TrimmedSchema, v.minLength(1), v.maxLength(MAX_CONFIG_CHANGE_REASON_CHARS)),
    objects: v.pipe(
      v.array(ConfigChangeObjectInputSchema),
      v.minLength(1),
      v.maxLength(MAX_CONFIG_CHANGE_OBJECTS),
    ),
  }),
  v.check(
    (r) => new Set(r.objects.map((o) => configObjectKey(o.kind, o.name))).size === r.objects.length,
    'an object appears twice',
  ),
);
export type SessionConfigChangeCreate = v.InferOutput<typeof SessionConfigChangeCreateSchema>;

/** One object of a stored request, with what the diff and the staleness check were computed from. */
export const ConfigChangeObjectSchema = v.object({
  kind: ConfigObjectKindSchema,
  name: v.string(),
  /** The hash the proposal was written against; empty for a new file. */
  baseHash: v.string(),
  /** The hash of the new content; empty when the object is deleted. */
  newHash: v.string(),
  /** The new content; null when the object is deleted. */
  content: v.nullable(v.string()),
  /** A unified diff of the file as the agent read it at proposal time. */
  diff: v.string(),
  /** The directories writing the file creates, relative to the session's working directory. */
  newDirectories: v.optional(v.array(v.string())),
});
export type ConfigChangeObject = v.InferOutput<typeof ConfigChangeObjectSchema>;

/** A session's request to change its own configuration (ADR 0036). */
export const SessionConfigChangeSchema = v.object({
  changeId: v.string(),
  sessionId: v.string(),
  state: ConfigChangeStateSchema,
  reason: v.string(),
  objects: v.array(ConfigChangeObjectSchema),
  createdAt: v.number(),
  /** When a request nobody decided expires. */
  expiresAt: v.number(),
  decidedAt: v.nullable(v.number()),
  appliedAt: v.nullable(v.number()),
  undoneAt: v.nullable(v.number()),
  /** Whether undo is possible: applied, and the previous content is still kept. */
  undoable: v.boolean(),
  /** The error code of a `failed` request, or of the last refused undo (`stale`, `owner-offline`, ...). */
  error: v.optional(v.string()),
  errorReason: v.optional(v.string()),
});
export type SessionConfigChange = v.InferOutput<typeof SessionConfigChangeSchema>;

/** GET /api/sessions/:sessionId/config-changes: the newest first. */
export const SessionConfigChangeListSchema = v.object({
  changes: v.array(SessionConfigChangeSchema),
});
export type SessionConfigChangeList = v.InferOutput<typeof SessionConfigChangeListSchema>;
