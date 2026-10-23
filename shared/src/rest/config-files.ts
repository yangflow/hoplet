import * as v from 'valibot';
import { CONFIG_FILE_NAME_PATTERN } from './config-changes.ts';

/**
 * Reading a session's own configuration files (ADR 0036): the project's memory file, its skills and commands,
 * and the user's. One request serves the session MCP tools today and the composer's skill list and the
 * session's settings later. An answer is one page of at most `CONFIG_PAGE_BYTES`; the paths are fixed by the
 * kind, never taken from the caller.
 */
export const CONFIG_FILE_SCOPES = ['project', 'user'] as const;
export const ConfigFileScopeSchema = v.picklist(CONFIG_FILE_SCOPES);
export type ConfigFileScope = v.InferOutput<typeof ConfigFileScopeSchema>;

/** `list` names the skills and commands of a scope; the others read one file. */
export const CONFIG_READ_KINDS = ['memory', 'skill', 'command', 'list'] as const;
export const ConfigReadKindSchema = v.picklist(CONFIG_READ_KINDS);
export type ConfigReadKind = v.InferOutput<typeof ConfigReadKindSchema>;

/** The most bytes of text one answer carries (ADR 0007). */
export const CONFIG_PAGE_BYTES = 32 * 1024;
/** The most entries a list answer carries. */
export const MAX_CONFIG_LIST_ENTRIES = 200;
export const MAX_CONFIG_DESCRIPTION_CHARS = 300;

/** Why the agent did not answer: the provider has no such file, the request is malformed, the read failed. */
export const CONFIG_READ_REJECTIONS = ['unsupported', 'invalid', 'io'] as const;
export const ConfigReadRejectionSchema = v.picklist(CONFIG_READ_REJECTIONS);
export type ConfigReadRejection = v.InferOutput<typeof ConfigReadRejectionSchema>;

const byteCount = v.pipe(v.number(), v.integer(), v.minValue(0));

export const ConfigFileEntrySchema = v.strictObject({
  kind: v.picklist(['skill', 'command']),
  name: v.pipe(v.string(), v.regex(CONFIG_FILE_NAME_PATTERN)),
  description: v.pipe(v.string(), v.maxLength(MAX_CONFIG_DESCRIPTION_CHARS)),
});
export type ConfigFileEntry = v.InferOutput<typeof ConfigFileEntrySchema>;

/**
 * What the agent found. A file that does not exist is `exists` false, not an error. `size` and `hash` (SHA-256,
 * hex) describe the whole file; `text` starts at byte `offset` and `nextOffset` names the next page, absent on
 * the last one. A list answer carries `entries` instead of text.
 */
export const ConfigFileReadSchema = v.strictObject({
  kind: ConfigReadKindSchema,
  scope: ConfigFileScopeSchema,
  name: v.optional(v.string()),
  exists: v.boolean(),
  size: byteCount,
  hash: v.string(),
  offset: byteCount,
  text: v.string(),
  nextOffset: v.optional(byteCount),
  entries: v.array(ConfigFileEntrySchema),
});
export type ConfigFileRead = v.InferOutput<typeof ConfigFileReadSchema>;

/** `GET /api/sessions/:id/config-files`: `scope` is `project` when absent. */
export const ConfigFileListQuerySchema = v.strictObject({
  scope: v.optional(ConfigFileScopeSchema),
});
export type ConfigFileListQuery = v.InferOutput<typeof ConfigFileListQuerySchema>;

/** `GET /api/sessions/:id/config-files/:kind`: `name` is required for `skill` and `command`. */
export const ConfigFileReadQuerySchema = v.strictObject({
  scope: v.optional(ConfigFileScopeSchema),
  name: v.optional(v.pipe(v.string(), v.maxLength(64))),
  offset: v.optional(v.pipe(v.string(), v.regex(/^\d{1,9}$/), v.transform(Number))),
});
export type ConfigFileReadQuery = v.InferOutput<typeof ConfigFileReadQuerySchema>;

/** `GET /api/sessions/:id/config`: the session's options as its runtime reports them. */
export const SessionConfigViewSchema = v.strictObject({
  provider: v.string(),
  state: v.string(),
  options: v.array(
    v.strictObject({
      id: v.string(),
      name: v.string(),
      type: v.picklist(['select', 'boolean']),
      currentValue: v.union([v.string(), v.boolean()]),
      values: v.optional(v.array(v.string())),
    }),
  ),
});
export type SessionConfigView = v.InferOutput<typeof SessionConfigViewSchema>;
