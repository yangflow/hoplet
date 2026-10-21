// What a notification says about a request: a short, redacted line, never content (ADR 0021).
import { scrubText } from '../scrub/index.ts';

/** The longest summary a notification carries. */
export const SUMMARY_MAX = 80;
/** The longest summary the action page shows. */
export const PAGE_SUMMARY_MAX = 400;

/** Names of tool inputs that hold what the tool is about to touch, in the order they are tried. */
const SUMMARY_FIELDS = ['command', 'file_path', 'notebook_path', 'path', 'pattern', 'url'] as const;

/** Replaces what looks like a credential in a command or path; the result is for people, not for running. */
export function redactText(text: string): string {
  return scrubText(text);
}

/** One line of at most `max` characters: whitespace collapsed, credentials masked, cut with an ellipsis. */
export function clip(text: string, max: number): string {
  const line = redactText(text).replace(/\s+/g, ' ').trim();
  const chars = [...line];
  return chars.length <= max ? line : `${chars.slice(0, max - 1).join('')}…`;
}

/**
 * The line a request is summarized by: the Bash command or the path of a file tool. An MCP tool or an input of
 * an unknown shape has none, only its tool name is shown.
 */
export function summarize(tool: string, input: unknown, max: number): string | undefined {
  if (tool.startsWith('mcp__')) return undefined;
  if (typeof input !== 'object' || input === null) return undefined;
  const fields = input as Record<string, unknown>;
  for (const name of SUMMARY_FIELDS) {
    const value = fields[name];
    if (typeof value === 'string' && value.trim() !== '') return clip(value, max);
  }
  return undefined;
}
