import * as v from 'valibot';
import { OPAQUE_ID_PATTERN } from '../ids.ts';
import { ConfigurationIdSchema, SessionConfigSchema } from '../runtime.ts';
import { ProviderSchema } from '../session.ts';
import { MAX_TEMPLATE_TEXT_CHARS } from '../templates.ts';
import { TrimmedSchema } from './common.ts';
import { NewSessionResultSchema } from './sessions.ts';

/** The most templates the host can keep. */
export const MAX_TEMPLATES = 100;
/** The format version of an exported file; import refuses another. */
export const TEMPLATE_FILE_VERSION = 1;

export const TemplateIdSchema = v.pipe(v.string(), v.regex(OPAQUE_ID_PATTERN));

const NameSchema = v.pipe(TrimmedSchema, v.maxLength(80));
const TextSchema = v.pipe(TrimmedSchema, v.maxLength(MAX_TEMPLATE_TEXT_CHARS));
const PathSchema = v.pipe(TrimmedSchema, v.maxLength(4096));

/** POST and PUT /api/templates: the whole definition. The route also checks the placeholders of `text`. */
export const TemplateBodySchema = v.object({
  name: NameSchema,
  text: TextSchema,
  /** Left out: the starting window asks for the machine. */
  deviceId: v.optional(TrimmedSchema),
  projectPath: v.optional(PathSchema),
  provider: ProviderSchema,
  configuration: v.optional(ConfigurationIdSchema),
  config: v.optional(SessionConfigSchema),
});
export type TemplateBody = v.InferOutput<typeof TemplateBodySchema>;

export const TemplatePlaceholderSchema = v.object({
  name: v.string(),
  default: v.optional(v.string()),
});

export const TemplateSchema = v.object({
  id: TemplateIdSchema,
  name: v.string(),
  text: v.string(),
  deviceId: v.optional(v.string()),
  projectPath: v.optional(v.string()),
  provider: ProviderSchema,
  configuration: v.optional(ConfigurationIdSchema),
  config: v.optional(SessionConfigSchema),
  /** In the order of first use. */
  placeholders: v.array(TemplatePlaceholderSchema),
  createdAt: v.number(),
  updatedAt: v.number(),
});
export type Template = v.InferOutput<typeof TemplateSchema>;

export const TemplateListResponseSchema = v.object({ templates: v.array(TemplateSchema) });
export type TemplateListResponse = v.InferOutput<typeof TemplateListResponseSchema>;

/** POST /api/templates/:id/start. The same `promptId` rule and the same result as `POST /api/sessions`. */
export const TemplateStartRequestSchema = v.object({
  /** Placeholder name to value, as typed. */
  values: v.optional(
    v.record(v.string(), v.pipe(v.string(), v.maxLength(MAX_TEMPLATE_TEXT_CHARS))),
    {},
  ),
  promptId: v.pipe(v.string(), v.minLength(1), v.maxLength(128)),
  /** Fills what the template left out, or replaces it for this start. */
  deviceId: v.optional(TrimmedSchema),
  projectPath: v.optional(PathSchema),
});
export type TemplateStartRequest = v.InferOutput<typeof TemplateStartRequestSchema>;
export const TemplateStartResultSchema = NewSessionResultSchema;

/** One template of an export file: a machine is its name, because an ID means nothing on another Hoplet. */
export const TemplateFileEntrySchema = v.strictObject({
  name: NameSchema,
  text: TextSchema,
  device: v.optional(TrimmedSchema),
  project: v.optional(PathSchema),
  provider: ProviderSchema,
  configuration: v.optional(ConfigurationIdSchema),
  config: v.optional(SessionConfigSchema),
});
export type TemplateFileEntry = v.InferOutput<typeof TemplateFileEntrySchema>;

/** GET /api/templates/export and the file of an import. */
export const TemplateFileSchema = v.strictObject({
  v: v.literal(TEMPLATE_FILE_VERSION),
  templates: v.pipe(v.array(TemplateFileEntrySchema), v.maxLength(MAX_TEMPLATES)),
});
export type TemplateFile = v.InferOutput<typeof TemplateFileSchema>;

/**
 * POST /api/templates/import. `machines` maps the `device` name of an entry that matches no machine to the ID of
 * one the user chose. With `dryRun` nothing is stored and the response says what would be added.
 */
export const TemplateImportRequestSchema = v.object({
  file: TemplateFileSchema,
  machines: v.optional(v.record(v.string(), TrimmedSchema)),
  dryRun: v.optional(v.boolean(), false),
});
export type TemplateImportRequest = v.InferOutput<typeof TemplateImportRequestSchema>;

export const TemplateImportItemSchema = v.object({
  /** The name the template has or would have; a taken one has a suffix (` (2)`). */
  name: v.string(),
  renamed: v.boolean(),
  /** The `device` of the entry, when it named one. */
  device: v.optional(v.string()),
  /** The machine it resolved to; absent when none matched and `machines` named none. */
  deviceId: v.optional(v.string()),
});
export const TemplateImportResultSchema = v.object({
  dryRun: v.boolean(),
  items: v.array(TemplateImportItemSchema),
  /** The templates stored; empty on a dry run. */
  templates: v.array(TemplateSchema),
});
export type TemplateImportResult = v.InferOutput<typeof TemplateImportResultSchema>;
