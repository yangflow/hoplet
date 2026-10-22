import type { Provider } from '@hoplet/shared';

/** A tool call a session line holds, as the audit log records it (ADR 0030). */
export interface ToolCallRecord {
  /** Index of the line in the batch it came from. */
  line: number;
  /** Unix ms of the line, or the fallback. */
  at: number;
  /** The vendor's tool use ID; may be empty. */
  id: string;
  tool: string;
  /** The shell command of a shell tool, unscrubbed and cut to `COMMAND_READ_CHARS`; the deriver scrubs and cuts it. */
  command?: string;
  /** What the call changes: at most `PATHS_MAX` paths. */
  paths?: string[];
  /** How many paths the call changes, also when `paths` was cut. */
  pathCount?: number;
}

/** The most of a command the extractor keeps: more than a record holds, so the scrubber sees a secret whole. */
export const COMMAND_READ_CHARS = 4000;
/** The most paths a record names. */
export const PATHS_MAX = 20;

type Rec = Record<string, unknown>;

const isRec = (value: unknown): value is Rec =>
  !!value && typeof value === 'object' && !Array.isArray(value);
const str = (value: unknown): string | undefined => (typeof value === 'string' ? value : undefined);

function parseObject(raw: string): Rec | null {
  try {
    const o = JSON.parse(raw);
    return isRec(o) ? o : null;
  } catch {
    return null;
  }
}

function timeOf(v: unknown, fallback: number): number {
  if (typeof v !== 'string') return fallback;
  const t = Date.parse(v);
  return Number.isFinite(t) ? t : fallback;
}

function cut(text: string): string {
  const chars = [...text];
  return chars.length > COMMAND_READ_CHARS
    ? `${chars.slice(0, COMMAND_READ_CHARS).join('')}`
    : text;
}

function withPaths(record: ToolCallRecord, paths: string[]): ToolCallRecord {
  if (paths.length === 0) return record;
  return { ...record, paths: paths.slice(0, PATHS_MAX), pathCount: paths.length };
}

/**
 * The tool calls the appended lines hold, without changing them, by the vendor they come from. The dispatch is
 * where a vendor's line shapes plug in, beside `extractUsage`; the shapes mirror the vendor mappings in
 * `api/projection/`, which the store may not import.
 */
export function extractToolCalls(
  provider: Provider,
  lines: string[],
  fallbackAt: number,
): ToolCallRecord[] {
  return provider === 'codex'
    ? extractCodexToolCalls(lines, fallbackAt)
    : extractClaudeToolCalls(lines, fallbackAt);
}

/**
 * The command a shell tool runs and the files an edit tool changes, from a tool's input as Claude Code writes
 * it. A permission request names the same tools, so it uses this too. The command is cut, not scrubbed.
 */
export function toolInputSummary(
  tool: string,
  input: unknown,
): { command?: string; paths: string[] } {
  const rec = isRec(input) ? input : {};
  const out: { command?: string; paths: string[] } = { paths: [] };
  const command = tool === 'Bash' || tool === 'exec_command' ? str(rec.command) : undefined;
  if (command !== undefined) out.command = cut(command);
  const path =
    tool === 'Edit' || tool === 'MultiEdit' || tool === 'Write'
      ? str(rec.file_path)
      : tool === 'NotebookEdit'
        ? (str(rec.notebook_path) ?? str(rec.file_path))
        : undefined;
  if (path) out.paths.push(path);
  return out;
}

/** Claude Code: a `tool_use` block of an assistant message. Edit-like tools name the file they change. */
function extractClaudeToolCalls(lines: string[], fallbackAt: number): ToolCallRecord[] {
  const out: ToolCallRecord[] = [];
  lines.forEach((raw, line) => {
    if (!raw.includes('"tool_use"')) return;
    const o = parseObject(raw);
    const message = o?.message;
    if (!isRec(message) || !Array.isArray(message.content)) return;
    const at = timeOf(o?.timestamp, fallbackAt);
    for (const block of message.content) {
      if (!isRec(block) || block.type !== 'tool_use') continue;
      const tool = str(block.name) ?? 'tool';
      const input = isRec(block.input) ? block.input : {};
      const { command, paths } = toolInputSummary(tool, input);
      const record: ToolCallRecord = {
        line,
        at,
        id: str(block.id) ?? '',
        tool,
        ...(command !== undefined ? { command } : {}),
      };
      out.push(withPaths(record, paths));
    }
  });
  return out;
}

/** The command a `CommandExecution` shows: the script of a `-lc` wrapper, else the joined argv. */
function codexCommand(item: Rec): string {
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

/**
 * Codex: the `item_completed` events. A `CommandExecution` is a shell call, a `FileChange` names the files it
 * changes and an `McpToolCall` an MCP tool (`mcp__<server>__<tool>`).
 */
function extractCodexToolCalls(lines: string[], fallbackAt: number): ToolCallRecord[] {
  const out: ToolCallRecord[] = [];
  lines.forEach((raw, line) => {
    if (!raw.includes('item_completed')) return;
    const o = parseObject(raw);
    if (o?.type !== 'event_msg' || !isRec(o.payload) || o.payload.type !== 'item_completed') return;
    const item = o.payload.item;
    if (!isRec(item)) return;
    const at = timeOf(o.timestamp, fallbackAt);
    const id = str(item.id) ?? '';
    switch (item.type) {
      case 'CommandExecution':
        out.push({ line, at, id, tool: 'exec_command', command: cut(codexCommand(item)) });
        break;
      case 'FileChange':
        out.push(
          withPaths(
            { line, at, id, tool: 'apply_patch' },
            isRec(item.changes) ? Object.keys(item.changes) : [],
          ),
        );
        break;
      case 'McpToolCall':
        out.push({
          line,
          at,
          id,
          tool: `mcp__${str(item.server) ?? ''}__${str(item.tool) ?? 'tool'}`,
        });
        break;
      default:
        break;
    }
  });
  return out;
}
