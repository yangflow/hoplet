/**
 * Tool calls as the conversation shows them (docs/design.md#layout-and-breakpoints): one row of icon, past-tense
 * verb, object and meta, and the output that opens under it. The kind the session provider's mapping gives a
 * call picks its verb and icon; the meta holds only what the call and its result carry, never a guess.
 */
import type { ToolKind, ToolMcp } from '@hoplet/shared';
import type { IconName } from '../../components/icons.tsx';
import { t } from '../../lib/i18n.ts';
import { formatBytes, formatDuration } from './format.ts';

export type { ToolKind } from '@hoplet/shared';

/** A tool as a call names it: the vendor's name and the kind its mapping gives it. */
export type ToolRef = { name: string; kind: ToolKind; mcp?: ToolMcp | undefined };

export type ToolSpec = {
  kind: ToolKind;
  /** The English verb, which is also the key of its translation; a tool of kind `other` is its own verb. */
  verb: string;
  icon: IconName;
  /** The server and tool of an MCP call. */
  mcp?: ToolMcp;
};

const KINDS: Record<Exclude<ToolKind, 'other'>, { verb: string; icon: IconName }> = {
  read: { verb: 'Read', icon: 'read' },
  edit: { verb: 'Edited', icon: 'pen' },
  write: { verb: 'Wrote', icon: 'write' },
  run: { verb: 'Ran', icon: 'terminal' },
  search: { verb: 'Searched', icon: 'search' },
  list: { verb: 'Listed', icon: 'folder' },
  fetch: { verb: 'Fetched', icon: 'globe' },
  call: { verb: 'Called', icon: 'plug' },
  view: { verb: 'Viewed', icon: 'image' },
};

/** The tool a stored or live row names. */
export function toolRefOf(row: { tool: string; toolKind: ToolKind; mcp?: ToolMcp }): ToolRef {
  return { name: row.tool, kind: row.toolKind, ...(row.mcp ? { mcp: row.mcp } : {}) };
}

/** The verb and icon of a tool by its kind; a tool of kind `other` is named by its own name. */
export function toolSpec(tool: ToolRef): ToolSpec {
  const { kind, mcp } = tool;
  if (kind === 'other') return { kind, verb: tool.name.trim() || t('Tool'), icon: 'plug' };
  return { kind, ...KINDS[kind], ...(kind === 'call' && mcp ? { mcp } : {}) };
}

/** The verb as shown: translated when it is one of the table's, else the tool's own name. */
export function verbLabel(spec: ToolSpec): string {
  return spec.kind === 'other' ? spec.verb : t(spec.verb);
}

/** The distinct verbs of some tools, in order of first use (the folded row names them). */
export function verbsOf(tools: ToolRef[]): string[] {
  const seen = new Set<string>();
  for (const tool of tools) seen.add(verbLabel(toolSpec(tool)));
  return [...seen];
}

/** What the object column shows for a call: the pattern as a quoted query, the URL as its host, MCP as `server › tool`. */
export function toolObject(spec: ToolSpec, arg: string): string {
  switch (spec.kind) {
    case 'search':
      return arg === '' ? '' : `"${arg}"`;
    case 'fetch':
      try {
        return new URL(arg).host || arg;
      } catch {
        return arg;
      }
    case 'call':
      return spec.mcp ? `${spec.mcp.server} › ${spec.mcp.tool}` : arg;
    default:
      return arg;
  }
}

/** A call with what its result carries, in the shape both stored and live rows reduce to. */
export type ToolFacts = ToolRef & {
  input: unknown;
  result?: { text: string; failed: boolean; bytes: number; elided?: boolean } | undefined;
  /** Milliseconds since the epoch, when the call and its result are timed. */
  startedAt?: number | undefined;
  endedAt?: number | undefined;
};

function record(input: unknown): Record<string, unknown> {
  return input && typeof input === 'object' ? (input as Record<string, unknown>) : {};
}

function num(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

function plural(n: number, one: string, many: string): string {
  return t(n === 1 ? one : many).replace('{n}', n.toLocaleString());
}

/** A time span as `0.1 s`, `42 s` or `1m 08s`. */
export function formatSpan(ms: number): string {
  if (ms >= 60_000) return formatDuration(ms);
  const seconds = ms / 1000;
  const shown =
    seconds < 10 ? (Math.round(seconds * 10) / 10).toFixed(1) : String(Math.round(seconds));
  return t('{n} s').replace('{n}', shown);
}

/** The time between a call and its result, when both are timed. */
export function spanOf(facts: Pick<ToolFacts, 'startedAt' | 'endedAt'>): number | null {
  const { startedAt, endedAt } = facts;
  if (startedAt === undefined || endedAt === undefined) return null;
  const span = endedAt - startedAt;
  return Number.isFinite(span) && span >= 0 ? span : null;
}

/** A read result is `cat -n` output: a line number, an arrow, the line. */
const NUMBERED = /^\s*(\d+)→(.*)$/;

/** The numbered lines of a read result, or null when the text is not in that shape. */
export function numberedLines(text: string): { no: number; text: string }[] | null {
  const lines = text.split('\n');
  if (lines[lines.length - 1] === '') lines.pop();
  const out: { no: number; text: string }[] = [];
  for (const line of lines) {
    const m = NUMBERED.exec(line);
    if (!m) return null;
    out.push({ no: Number(m[1]), text: m[2] ?? '' });
  }
  return out.length > 0 ? out : null;
}

function readRange(facts: ToolFacts): string | null {
  const rec = record(facts.input);
  const offset = num(rec.offset);
  const limit = num(rec.limit);
  const result = facts.result;
  const parsed = result ? numberedLines(result.text) : null;
  const first = parsed?.[0]?.no ?? offset ?? (limit !== undefined ? 1 : undefined);
  const last = result?.elided
    ? first !== undefined && limit !== undefined
      ? first + limit - 1
      : undefined
    : (parsed?.[parsed.length - 1]?.no ??
      (first !== undefined && limit !== undefined ? first + limit - 1 : undefined));
  if (first === undefined || last === undefined) return null;
  return `L${first.toLocaleString()}–${last.toLocaleString()}`;
}

/** The exit code a shell result states (`Exit code 1`); a result that is not an error is a clean exit. */
export function exitCode(result: NonNullable<ToolFacts['result']>): number | null {
  const m = /^Exit code (-?\d+)/.exec(result.text);
  if (m) return Number(m[1]);
  return result.failed ? null : 0;
}

function searchCount(facts: ToolFacts): string | null {
  const result = facts.result;
  if (!result || result.elided) return null;
  const rec = record(facts.input);
  const text = result.text.trim();
  const found = /^Found (\d+) (?:files?|matches|match|lines?)\b/.exec(text);
  const body = text.split('\n').filter((l) => l.trim() !== '' && !/^Found \d+/.test(l));
  const none = /^No (?:matches|files)/i.test(text);
  const n = found ? Number(found[1]) : none ? 0 : body.length;
  const name = facts.name.toLowerCase();
  if (name === 'websearch') return null;
  const byFile =
    name === 'glob' || rec.output_mode === 'files_with_matches' || /^Found \d+ files?/.test(text);
  return byFile ? plural(n, '{n} file', '{n} files') : plural(n, '{n} match', '{n} matches');
}

function listCount(facts: ToolFacts): string | null {
  const result = facts.result;
  if (!result || result.elided) return null;
  const entries = result.text.split('\n').filter((l) => /^\s+- /.test(l)).length;
  return entries > 0 ? plural(entries, '{n} entry', '{n} entries') : null;
}

function fetchStatus(facts: ToolFacts): string | null {
  const result = facts.result;
  if (!result?.failed) return null;
  const m = /\b(?:status(?: code)?|HTTP)\s*:?\s*(\d{3})\b/i.exec(result.text);
  return m?.[1] ?? null;
}

function callItems(facts: ToolFacts): string | null {
  const result = facts.result;
  if (!result || result.elided || result.failed) return null;
  try {
    const parsed: unknown = JSON.parse(result.text);
    return Array.isArray(parsed) ? plural(parsed.length, '{n} item', '{n} items') : null;
  } catch {
    return null;
  }
}

/**
 * The meta of a finished call, right-aligned in its row. Each part is present only when the call or its result
 * states it: the line range and size of a read, the exit code and duration of a command, the matches of a search.
 */
export function metaParts(facts: ToolFacts): string[] {
  const spec = toolSpec(facts);
  const result = facts.result;
  const size = result ? formatBytes(result.bytes) : null;
  const span = spanOf(facts);
  const time = span === null ? null : formatSpan(span);
  const parts: (string | null)[] = [];
  switch (spec.kind) {
    case 'read':
      parts.push(readRange(facts), size);
      break;
    case 'write': {
      const content = record(facts.input).content;
      if (typeof content === 'string') {
        const lines =
          content === '' ? 0 : content.split('\n').length - (content.endsWith('\n') ? 1 : 0);
        parts.push(
          plural(lines, '{n} line', '{n} lines'),
          formatBytes(new TextEncoder().encode(content).length),
        );
      }
      break;
    }
    case 'run': {
      if (!result) break;
      const code = exitCode(result);
      parts.push(code === null ? t('Failed') : t('exit {n}').replace('{n}', String(code)), time);
      break;
    }
    case 'search':
      parts.push(searchCount(facts));
      break;
    case 'list':
      parts.push(listCount(facts));
      break;
    case 'fetch':
      parts.push(fetchStatus(facts), size);
      break;
    case 'call':
      parts.push(callItems(facts) ?? size, time);
      break;
    default:
      parts.push(size);
  }
  if (result?.failed && spec.kind !== 'run') parts.unshift(t('Failed'));
  return parts.filter((p): p is string => p !== null);
}

/** The size of an embedded image from its base64 data. */
export function base64Bytes(data: string): number {
  const padding = data.endsWith('==') ? 2 : data.endsWith('=') ? 1 : 0;
  return Math.max(0, Math.floor((data.length * 3) / 4) - padding);
}
