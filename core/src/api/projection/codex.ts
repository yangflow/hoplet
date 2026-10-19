/**
 * The Codex mapping (ADR 0012, ADR 0027): one stored rollout line onto at most one conversation entry, and
 * Codex's tool names onto Hoplet's tool kinds. A rollout holds the conversation twice: as `response_item`
 * records, the model's raw input and output (injected context, developer messages, tool calls as JavaScript
 * for the `exec` tool), and as `event_msg` records of type `item_completed`, the items Codex's own clients
 * render. The items are the semantic one and are all this mapping reads; `response_item` and every other
 * record type, `session_meta` with its base instructions among them, map to nothing. A record or an item the
 * mapping does not know is ignored, never an error.
 */
import type { Entry, FullBlock, Role, ToolKind, ToolMcp } from '@hoplet/shared';

type Rec = Record<string, unknown>;

const isRec = (value: unknown): value is Rec =>
  !!value && typeof value === 'object' && !Array.isArray(value);

const str = (value: unknown): string | undefined => (typeof value === 'string' ? value : undefined);

/** Codex's tools by kind, as its items and its app server name them; a tool not named here is `other`. */
const TOOL_KINDS = new Map<string, ToolKind>([
  ['exec_command', 'run'],
  ['write_stdin', 'run'],
  ['shell', 'run'],
  ['local_shell', 'run'],
  ['commandExecution', 'run'],
  ['apply_patch', 'edit'],
  ['fileChange', 'edit'],
  ['web_search', 'search'],
  ['webSearch', 'search'],
]);

/** An MCP tool as the mapping names it: `mcp__<server>__<tool>`. */
const MCP_TOOL = /^mcp__(.+?)__(.+)$/;

/** The kind of a Codex tool; an MCP tool is a `call` with its server and tool. */
export function classifyCodexTool(name: string): { kind: ToolKind; mcp?: ToolMcp } {
  const mcp = MCP_TOOL.exec(name);
  if (mcp) return { kind: 'call', mcp: { server: mcp[1] ?? '', tool: mcp[2] ?? '' } };
  return { kind: TOOL_KINDS.get(name) ?? 'other' };
}

function entry(lineNo: number, role: Role, line: Rec, blocks: FullBlock[]): Entry | null {
  if (blocks.length === 0) return null;
  const ts = str(line.timestamp);
  return { lineNo, role, ...(ts ? { ts } : {}), blocks };
}

const text = (value: string): FullBlock[] => (value.trim() ? [{ type: 'text', text: value }] : []);

function toolCall(id: string, name: string, input: unknown, kind?: ToolKind): FullBlock {
  return {
    type: 'tool-call',
    id,
    name,
    ...classifyCodexTool(name),
    ...(kind ? { kind } : {}),
    input,
  };
}

function toolResult(id: string, content: string, isError: boolean): FullBlock {
  return { type: 'tool-result', id, content, ...(isError ? { isError: true } : {}) };
}

/** Statuses of an item that did not run to completion. */
const FAILED_STATUSES = new Set([
  'failed',
  'declined',
  'rejected',
  'cancelled',
  'canceled',
  'error',
]);
const failedStatus = (status: unknown): boolean =>
  typeof status === 'string' && FAILED_STATUSES.has(status.toLowerCase());

/** A user message's content part: text, an image, or any other part by name. */
function userPart(raw: unknown): FullBlock | null {
  if (!isRec(raw)) return null;
  const type = str(raw.type) ?? 'unknown';
  if (type === 'text' || type === 'input_text') return text(str(raw.text) ?? '')[0] ?? null;
  if (type === 'image' || type === 'input_image') {
    const url = str(raw.image_url) ?? str(raw.url);
    if (!url) return { type: 'other', kind: type, text: JSON.stringify(raw) };
    const data = /^data:([^;,]+);base64,(.*)$/s.exec(url);
    return data
      ? { type: 'image', mediaType: data[1] ?? 'image', data: data[2] ?? '' }
      : { type: 'image', mediaType: 'image', url };
  }
  if (type === 'local_image' || type === 'localImage') {
    const path = str(raw.path);
    return { type: 'image', mediaType: 'image', ...(path ? { url: path } : {}) };
  }
  return { type: 'other', kind: type, text: JSON.stringify(raw) };
}

/** The command a `CommandExecution` shows: the script of a `-lc` wrapper, else the joined argv. */
function displayCommand(item: Rec): string {
  const argv = Array.isArray(item.command)
    ? item.command.filter((a): a is string => typeof a === 'string')
    : [];
  if (
    argv.length >= 3 &&
    /(^|\/)(ba|z|fi|da)?sh$/.test(argv[0] ?? '') &&
    /^-\w*c$/.test(argv[1] ?? '')
  ) {
    return argv.slice(2).join(' ');
  }
  if (argv.length > 0) return argv.join(' ');
  if (Array.isArray(item.parsed_cmd)) {
    for (const p of item.parsed_cmd) if (isRec(p) && str(p.cmd)) return str(p.cmd) ?? '';
  }
  return '';
}

function commandExecution(item: Rec): FullBlock[] {
  const id = str(item.id) ?? '';
  const cwd = str(item.cwd);
  const input: Rec = { command: displayCommand(item) };
  if (cwd) input.cwd = cwd.replace(/^file:\/\//, '');
  const exit = typeof item.exit_code === 'number' ? item.exit_code : undefined;
  const declined = failedStatus(item.status);
  const failed = declined || (exit !== undefined && exit !== 0);
  const header = exit !== undefined && exit !== 0 ? `Exit code ${exit}\n` : '';
  const content = str(item.aggregated_output) || (declined ? (str(item.status) ?? '') : '');
  return [toolCall(id, 'exec_command', input), toolResult(id, header + content, failed)];
}

/** Old and new text of a unified diff, from its context, removed and added lines. */
function sidesOfDiff(diff: string): { old: string; next: string } {
  const old: string[] = [];
  const next: string[] = [];
  for (const line of diff.split('\n')) {
    if (line.startsWith('@@') || line.startsWith('---') || line.startsWith('+++')) continue;
    if (line.startsWith('+')) next.push(line.slice(1));
    else if (line.startsWith('-')) old.push(line.slice(1));
    else if (line.startsWith(' ')) {
      old.push(line.slice(1));
      next.push(line.slice(1));
    }
  }
  return { old: old.join('\n'), next: next.join('\n') };
}

/** A `FileChange`: one write or edit call and result per changed file. */
function fileChange(item: Rec): FullBlock[] {
  const id = str(item.id) ?? '';
  const changes = isRec(item.changes) ? Object.entries(item.changes) : [];
  const failed = failedStatus(item.status);
  const output = [str(item.stdout), str(item.stderr)].filter((s) => s?.trim()).join('\n');
  const blocks: FullBlock[] = [];
  changes.forEach(([path, change], i) => {
    const rec = isRec(change) ? change : {};
    const callId = changes.length > 1 ? `${id}#${i}` : id;
    const kind = str(rec.type);
    let input: Rec;
    let toolKind: ToolKind = 'edit';
    if (kind === 'add') {
      toolKind = 'write';
      input = { file_path: path, content: str(rec.content) ?? '' };
    } else if (kind === 'delete') {
      input = { file_path: path, delete: true };
    } else {
      input = { file_path: path };
      const diff = str(rec.unified_diff);
      if (diff !== undefined) {
        const sides = sidesOfDiff(diff);
        input.old_string = sides.old;
        input.new_string = sides.next;
      }
      if (str(rec.move_path)) input.move_path = rec.move_path;
    }
    blocks.push(toolCall(callId, 'apply_patch', input, toolKind));
    blocks.push(toolResult(callId, output || (str(item.status) ?? ''), failed));
  });
  return blocks;
}

/** An MCP result's content as text: text parts joined by newlines, other parts by name. */
function mcpResultText(result: Rec): string {
  const content = result.content;
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

function mcpToolCall(item: Rec): FullBlock[] {
  const id = str(item.id) ?? '';
  const server = str(item.server) ?? '';
  const tool = str(item.tool) ?? 'tool';
  const result = isRec(item.result) ? item.result : {};
  const error = isRec(item.error) ? (str(item.error.message) ?? JSON.stringify(item.error)) : '';
  const failed = result.isError === true || failedStatus(item.status) || error !== '';
  const call: FullBlock = {
    type: 'tool-call',
    id,
    name: `mcp__${server}__${tool}`,
    kind: 'call',
    mcp: { server, tool },
    input: item.arguments ?? {},
  };
  return [call, toolResult(id, mcpResultText(result) || error, failed)];
}

/** An `Extension` item: a web search is a search call, any other kind an `other` call. */
function extension(item: Rec): FullBlock[] {
  const id = str(item.id) ?? '';
  const kind = str(item.kind) ?? 'extension';
  const action = isRec(item.action) ? item.action : {};
  const results = Array.isArray(item.results) ? item.results : [];
  const content = results.length > 0 ? JSON.stringify(results) : '';
  if (kind === 'web_search') {
    const input: Rec = { query: str(item.query) ?? '' };
    if (str(action.url)) input.url = action.url;
    return [toolCall(id, 'web_search', input), toolResult(id, content, false)];
  }
  const input: Rec = {};
  if (str(item.query)) input.query = item.query;
  if (Object.keys(action).length > 0) input.action = action;
  return [toolCall(id, kind, input, 'other'), toolResult(id, content, false)];
}

function itemEntry(lineNo: number, line: Rec, item: Rec): Entry | null {
  switch (item.type) {
    case 'UserMessage': {
      const blocks: FullBlock[] = [];
      for (const part of Array.isArray(item.content) ? item.content : []) {
        const block = userPart(part);
        if (block) blocks.push(block);
      }
      return entry(lineNo, 'user', line, blocks);
    }
    case 'AgentMessage': {
      const blocks: FullBlock[] = [];
      for (const part of Array.isArray(item.content) ? item.content : []) {
        if (isRec(part)) blocks.push(...text(str(part.text) ?? ''));
      }
      return entry(lineNo, 'assistant', line, blocks);
    }
    case 'Reasoning': {
      const summary = (Array.isArray(item.summary_text) ? item.summary_text : [])
        .filter((s): s is string => typeof s === 'string' && !!s.trim())
        .join('\n\n');
      return entry(lineNo, 'assistant', line, summary ? [{ type: 'thinking', text: summary }] : []);
    }
    case 'CommandExecution':
      return entry(lineNo, 'assistant', line, commandExecution(item));
    case 'FileChange':
      return entry(lineNo, 'assistant', line, fileChange(item));
    case 'McpToolCall':
      return entry(lineNo, 'assistant', line, mcpToolCall(item));
    case 'Extension':
      return entry(lineNo, 'assistant', line, extension(item));
    // A plan-mode turn's plan is the assistant's Markdown, which clients render as text.
    case 'Plan':
      return entry(lineNo, 'assistant', line, text(str(item.text) ?? ''));
    default:
      return null;
  }
}

/** Maps one parsed stored line; `null` when the line holds nothing to show. */
export function mapCodexLine(line: unknown, lineNo: number): Entry | null {
  if (!isRec(line)) return null;
  if (line._parseError === true) {
    return entry(lineNo, 'system', {}, [
      { type: 'other', kind: 'unparsed', text: str(line.raw) ?? '' },
    ]);
  }
  if (line.type !== 'event_msg' || !isRec(line.payload)) return null;
  const payload = line.payload;
  switch (payload.type) {
    case 'item_completed':
      return isRec(payload.item) ? itemEntry(lineNo, line, payload.item) : null;
    case 'turn_aborted':
      return entry(
        lineNo,
        'system',
        line,
        text(
          payload.reason === 'interrupted'
            ? 'Turn interrupted'
            : `Turn aborted: ${str(payload.reason) ?? 'unknown reason'}`,
        ),
      );
    default:
      return null;
  }
}
