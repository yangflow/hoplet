/**
 * The Claude Code mapping (ADR 0012): one stored JSONL line onto one conversation entry, and Claude Code's
 * tool names onto Hoplet's tool kinds. Lines and blocks that carry no conversation (vendor bookkeeping and
 * internal records) map to nothing; a line the mapping does not know becomes an entry with an `other`
 * block, never an error.
 */
import type { Entry, FullBlock, Role, ToolKind, ToolMcp } from '@hoplet/shared';

type Rec = Record<string, unknown>;

const isRec = (value: unknown): value is Rec =>
  !!value && typeof value === 'object' && !Array.isArray(value);

const str = (value: unknown): string | undefined => (typeof value === 'string' ? value : undefined);

/** Line types that only record the vendor's own state. */
const BOOKKEEPING_TYPES = new Set([
  'last-prompt',
  'queue-operation',
  'mode',
  'file-history-snapshot',
  'ai-title',
  'permission-mode',
  'atis-latch',
  'cost-state',
]);

/** The attachment types a user attached on purpose; every other attachment is Claude Code's own control data. */
const USER_ATTACHMENTS = new Set(['file', 'image', 'pdf', 'directory']);

const BOOKKEEPING_SUBTYPES = new Set(['turn_duration', 'away_summary']);

/** Claude Code's internal records, such as `deferred_tools_record` or `deferred_tools_delta`. */
const isInternalRecord = (kind: string): boolean => /_(record|delta)$/.test(kind);

/** Claude Code's tools by kind; a tool not named here is `other`. */
const TOOL_KINDS = new Map<string, ToolKind>([
  ['Read', 'read'],
  ['NotebookRead', 'read'],
  ['Edit', 'edit'],
  ['MultiEdit', 'edit'],
  ['NotebookEdit', 'edit'],
  ['Write', 'write'],
  ['Bash', 'run'],
  ['BashOutput', 'run'],
  ['KillBash', 'run'],
  ['KillShell', 'run'],
  ['Grep', 'search'],
  ['Glob', 'search'],
  ['ToolSearch', 'search'],
  ['WebSearch', 'search'],
  ['LS', 'list'],
  ['WebFetch', 'fetch'],
]);

/** An MCP tool as Claude Code names it: `mcp__<server>__<tool>`. */
const MCP_TOOL = /^mcp__(.+?)__(.+)$/;

/** The kind of a Claude Code tool; an MCP tool is a `call` with its server and tool. */
export function classifyClaudeTool(name: string): { kind: ToolKind; mcp?: ToolMcp } {
  const mcp = MCP_TOOL.exec(name);
  if (mcp) return { kind: 'call', mcp: { server: mcp[1] ?? '', tool: mcp[2] ?? '' } };
  return { kind: TOOL_KINDS.get(name) ?? 'other' };
}

function entry(lineNo: number, role: Role, line: Rec, blocks: FullBlock[]): Entry | null {
  if (blocks.length === 0) return null;
  const ts = str(line.timestamp);
  return { lineNo, role, ...(ts ? { ts } : {}), blocks };
}

function other(kind: string, text: string, label?: string): FullBlock {
  return { type: 'other', kind, ...(label ? { label } : {}), text };
}

function imageBlock(block: Rec): FullBlock {
  const source = isRec(block.source) ? block.source : {};
  const mediaType = str(source.media_type) ?? str(block.media_type) ?? 'image';
  const data = str(source.data);
  const url = str(source.url) ?? str(block.file_path);
  return { type: 'image', mediaType, ...(data ? { data } : {}), ...(url ? { url } : {}) };
}

/** A tool result's content as text: a string as is, text parts joined by newlines, other parts by name. */
function resultText(content: unknown): string {
  if (typeof content === 'string') return content;
  if (!Array.isArray(content)) return '';
  const parts: string[] = [];
  for (const part of content) {
    if (!isRec(part)) continue;
    if (part.type === 'text' && typeof part.text === 'string') parts.push(part.text);
    else if (part.type === 'image') parts.push('[image]');
    else parts.push(`[${str(part.type) ?? 'content'}]`);
  }
  return parts.join('\n');
}

/** One content block of a user or assistant message. */
function contentBlock(raw: unknown): FullBlock | null {
  if (!isRec(raw)) return null;
  switch (raw.type) {
    case 'text':
      return typeof raw.text === 'string' && raw.text.trim()
        ? { type: 'text', text: raw.text }
        : null;
    case 'thinking':
      return typeof raw.thinking === 'string' && raw.thinking.trim()
        ? { type: 'thinking', text: raw.thinking }
        : null;
    case 'tool_use': {
      const name = str(raw.name) ?? 'tool';
      return {
        type: 'tool-call',
        id: str(raw.id) ?? '',
        name,
        ...classifyClaudeTool(name),
        input: raw.input ?? {},
      };
    }
    case 'tool_result':
      return {
        type: 'tool-result',
        id: str(raw.tool_use_id) ?? '',
        content: resultText(raw.content),
        ...(raw.is_error === true ? { isError: true } : {}),
      };
    case 'image':
      return imageBlock(raw);
    default: {
      const kind = str(raw.type) ?? 'unknown';
      return isInternalRecord(kind) ? null : other(kind, JSON.stringify(raw));
    }
  }
}

function messageBlocks(content: unknown): FullBlock[] {
  if (typeof content === 'string') {
    return content.trim() ? [{ type: 'text', text: content }] : [];
  }
  if (!Array.isArray(content)) return [];
  const blocks: FullBlock[] = [];
  for (const raw of content) {
    const block = contentBlock(raw);
    if (block) blocks.push(block);
  }
  return blocks;
}

function fileName(path: string): string {
  return path.split(/[/\\]/).pop() || path;
}

function attachmentLabel(att: Rec): string | undefined {
  for (const key of ['file_path', 'path', 'filename', 'name']) {
    const value = str(att[key])?.trim();
    if (value) return fileName(value);
  }
  return str(att.media_type)?.trim() || undefined;
}

/** Maps one parsed stored line; `null` when the line holds nothing to show. */
export function mapClaudeLine(line: unknown, lineNo: number): Entry | null {
  if (!isRec(line)) return entry(lineNo, 'system', {}, [other('unknown', JSON.stringify(line))]);
  if (line._parseError === true) {
    return entry(lineNo, 'system', {}, [other('unparsed', str(line.raw) ?? '')]);
  }
  const type = str(line.type);
  if (type && BOOKKEEPING_TYPES.has(type)) return null;
  const message = isRec(line.message) ? line.message : {};

  switch (type) {
    case 'user':
      if (line.isSidechain === true || line.isMeta === true) return null;
      return entry(lineNo, 'user', line, messageBlocks(message.content));
    case 'assistant':
      return entry(lineNo, 'assistant', line, messageBlocks(message.content));
    case 'system': {
      if (BOOKKEEPING_SUBTYPES.has(str(line.subtype) ?? '')) return null;
      const text = str(line.content) ?? str(line.message) ?? '';
      return entry(lineNo, 'system', line, text ? [{ type: 'text', text }] : []);
    }
    case 'attachment': {
      const att = isRec(line.attachment) ? line.attachment : {};
      const attType = str(att.type);
      const label = attachmentLabel(att);
      // An attachment without a type is shown when it names something.
      if (attType === undefined ? !label : !USER_ATTACHMENTS.has(attType)) return null;
      return entry(lineNo, 'system', line, [other('attachment', JSON.stringify(att), label)]);
    }
    default:
      if (type && isInternalRecord(type)) return null;
      return entry(lineNo, 'system', line, [other(type ?? 'unknown', JSON.stringify(line))]);
  }
}
