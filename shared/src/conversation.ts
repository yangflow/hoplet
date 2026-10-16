import * as v from 'valibot';

/**
 * The conversation model (ADR 0012): the shape clients render, whatever the vendor's line format.
 * A provider mapping turns one stored line into one entry; detail the model cannot express is `other`.
 */
export const BLOCK_TYPES = [
  'text',
  'thinking',
  'tool-call',
  'tool-result',
  'image',
  'other',
] as const;
export const BlockTypeSchema = v.picklist(BLOCK_TYPES);
export type BlockType = v.InferOutput<typeof BlockTypeSchema>;

export const TextBlockSchema = v.object({ type: v.literal('text'), text: v.string() });
export const ThinkingBlockSchema = v.object({ type: v.literal('thinking'), text: v.string() });
/**
 * Hoplet's own kinds of tool call, the closed set clients render a call by. A provider mapping classifies
 * each of its vendor's tools; ACP's ToolKind maps onto it (read → read, edit → edit or write, delete and
 * move → edit, search → search, execute → run, fetch → fetch, think and other → other).
 */
export const TOOL_KINDS = [
  'read',
  'edit',
  'write',
  'run',
  'search',
  'list',
  'fetch',
  'call',
  'view',
  'other',
] as const;
export const ToolKindSchema = v.picklist(TOOL_KINDS);
export type ToolKind = v.InferOutput<typeof ToolKindSchema>;

/** The server and the tool of a call (kind `call`) to a tool an MCP server provides. */
export const ToolMcpSchema = v.object({ server: v.string(), tool: v.string() });
export type ToolMcp = v.InferOutput<typeof ToolMcpSchema>;

export const ToolCallBlockSchema = v.object({
  type: v.literal('tool-call'),
  id: v.string(),
  /** The vendor's name for the tool, for display only */
  name: v.string(),
  kind: ToolKindSchema,
  mcp: v.optional(ToolMcpSchema),
  input: v.unknown(),
});
export const ToolResultBlockSchema = v.object({
  type: v.literal('tool-result'),
  /** The `id` of the tool call this answers */
  id: v.string(),
  content: v.string(),
  isError: v.optional(v.boolean()),
});
export const ImageBlockSchema = v.object({
  type: v.literal('image'),
  mediaType: v.string(),
  /** Base64 image data, when the line embeds it */
  data: v.optional(v.string()),
  /** Where the image lives, when the line refers to it instead */
  url: v.optional(v.string()),
});
export const OtherBlockSchema = v.object({
  type: v.literal('other'),
  /** The vendor's own name for the block or line, for display only */
  kind: v.string(),
  /** A short name for the block, such as a file name */
  label: v.optional(v.string()),
  text: v.optional(v.string()),
});

export const FullBlockSchema = v.variant('type', [
  TextBlockSchema,
  ThinkingBlockSchema,
  ToolCallBlockSchema,
  ToolResultBlockSchema,
  ImageBlockSchema,
  OtherBlockSchema,
]);

/**
 * A block over 8 KiB is replaced by this object (ADR 0007); `ref` addresses the full block at
 * `GET /api/sessions/:id/blocks/:line/:block`.
 */
export const ElidedBlockSchema = v.object({
  elided: v.literal(true),
  type: BlockTypeSchema,
  bytes: v.number(),
  /** Text preview of at most 1 KiB */
  preview: v.string(),
  ref: v.string(),
});

export const BlockSchema = v.union([FullBlockSchema, ElidedBlockSchema]);

export const ROLES = ['user', 'assistant', 'system'] as const;
export const RoleSchema = v.picklist(ROLES);

export const EntrySchema = v.object({
  lineNo: v.number(),
  role: RoleSchema,
  /** When the line was written, as the vendor recorded it (ISO 8601) */
  ts: v.optional(v.string()),
  /** A user message the model took in the middle of a turn, at a tool gap (ADR 0020). */
  steered: v.optional(v.literal(true)),
  blocks: v.array(BlockSchema),
});

export type FullBlock = v.InferOutput<typeof FullBlockSchema>;
export type ElidedBlock = v.InferOutput<typeof ElidedBlockSchema>;
export type Block = v.InferOutput<typeof BlockSchema>;
export type Role = v.InferOutput<typeof RoleSchema>;
export type Entry = v.InferOutput<typeof EntrySchema>;

/** The `ref` of an elided block: `<lineNo>/<blockIndex>` (blockIndex counts within `Entry.blocks`). */
export function blockRef(lineNo: number, blockIndex: number): string {
  return `${lineNo}/${blockIndex}`;
}
