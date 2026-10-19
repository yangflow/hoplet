/**
 * The read projection (ADR 0007, ADR 0012): the session provider's mapping turns a stored line into a
 * conversation entry and classifies its vendor's tools, and blocks over 8 KiB are elided. Computed on
 * read; nothing is stored.
 */
import type { Entry, FullBlock, Provider, ToolKind, ToolMcp } from '@hoplet/shared';
import { classifyClaudeTool, mapClaudeLine } from './claude.ts';
import { classifyCodexTool, mapCodexLine } from './codex.ts';
import { blockText, elideEntry } from './elide.ts';

export { ELIDE_ABOVE_BYTES, PREVIEW_BYTES } from './elide.ts';

/** What a vendor's mapping knows: its line format and its tool names. */
interface Mapping {
  /** Maps one parsed stored line; `null` when the line holds nothing to show. */
  line(line: unknown, lineNo: number): Entry | null;
  /** The kind of a tool by the vendor's name for it. */
  tool(name: string): ToolClass;
}

/** The kind of a tool and, for a tool an MCP server provides, its server and tool. */
export type ToolClass = { kind: ToolKind; mcp?: ToolMcp };

const MAPPINGS: Record<Provider, Mapping> = {
  claude: { line: mapClaudeLine, tool: classifyClaudeTool },
  codex: { line: mapCodexLine, tool: classifyCodexTool },
};

/** The entry of a stored line with every block in full, or `null` when the line holds nothing to show. */
export function fullEntry(provider: Provider, line: unknown, lineNo: number): Entry | null {
  return MAPPINGS[provider].line(line, lineNo);
}

/** The kind of a tool the session's runtime streams or asks permission for, as the mapping classifies it. */
export function classifyTool(provider: Provider, name: string): ToolClass {
  return MAPPINGS[provider].tool(name);
}

/** The entry of a stored line as the lines endpoint serves it: large blocks elided. */
export function projectLine(provider: Provider, line: unknown, lineNo: number): Entry | null {
  const entry = fullEntry(provider, line, lineNo);
  return entry ? elideEntry(entry) : null;
}

/** One full block of a stored line, or `null` when the line or the block does not exist. */
export function blockOfLine(
  provider: Provider,
  line: unknown,
  lineNo: number,
  index: number,
): FullBlock | null {
  const block = fullEntry(provider, line, lineNo)?.blocks[index];
  return block && !('elided' in block) ? block : null;
}

const EXCERPT_CHARS = 160;
const EXCERPT_LEAD = 40;

/**
 * A short single-line text of a matching line: the stretch around the first occurrence of `query` in the
 * entry's text, or the start of the entry's text when the match lies outside it.
 */
export function excerptOf(
  provider: Provider,
  line: unknown,
  lineNo: number,
  query: string,
): string {
  const entry = fullEntry(provider, line, lineNo);
  if (!entry) return '';
  const needle = query.toLowerCase();
  let first = '';
  for (const block of entry.blocks) {
    if ('elided' in block) continue;
    const text = blockText(block);
    if (!first && text.trim()) first = text;
    const at = text.toLowerCase().indexOf(needle);
    if (at >= 0) return collapse(text.slice(Math.max(0, at - EXCERPT_LEAD), at + EXCERPT_CHARS));
  }
  return collapse(first.slice(0, EXCERPT_CHARS));
}

function collapse(text: string): string {
  return text.replace(/\s+/g, ' ').trim().slice(0, EXCERPT_CHARS);
}
