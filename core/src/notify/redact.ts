// What a notification says about a request: a short, redacted line, never content (ADR 0021).

/** The longest summary a notification carries. */
export const SUMMARY_MAX = 80;
/** The longest summary the action page shows. */
export const PAGE_SUMMARY_MAX = 400;

const MASK = '***';

/** Names of tool inputs that hold what the tool is about to touch, in the order they are tried. */
const SUMMARY_FIELDS = ['command', 'file_path', 'notebook_path', 'path', 'pattern', 'url'] as const;

/** Replaces what looks like a credential in a command or path; the result is for people, not for running. */
export function redactText(text: string): string {
  return (
    text
      // user:password@ in a URL
      .replace(/(\b[a-z][a-z0-9+.-]*:\/\/)[^\s/@:]+:[^\s/@]+@/gi, `$1${MASK}@`)
      // Authorization headers and bearer credentials
      .replace(/\b(Bearer|Basic|Token)\s+[A-Za-z0-9._~+/=-]{6,}/gi, `$1 ${MASK}`)
      // NAME=value and --name value where the name says it is a credential
      .replace(
        /\b([A-Za-z0-9_.-]*(?:token|secret|passw(?:or)?d|passwd|api[_-]?key|apikey|auth|credential|private[_-]?key)[A-Za-z0-9_.-]*)(\s*[=:]\s*|\s+)(?:"[^"]*"|'[^']*'|\S+)/gi,
        (_all, name: string, sep: string) => `${name}${sep.trim() === '' ? ' ' : sep}${MASK}`,
      )
      // query-string credentials
      .replace(
        /([?&](?:token|key|secret|password|sig|signature|access_token|api_key)=)[^&\s]+/gi,
        `$1${MASK}`,
      )
      // well-known key shapes
      .replace(/\b(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}/g, MASK)
      .replace(/\b(?:ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]{16,}/g, MASK)
      .replace(/\bxox[abprs]-[A-Za-z0-9-]{10,}/g, MASK)
      .replace(/\bAKIA[0-9A-Z]{16}\b/g, MASK)
      .replace(/\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*/g, MASK)
      // a long unbroken run of key-like characters with a digit in it (a path segment follows a slash and is kept)
      .replace(/(?<![\w/.-])(?=[A-Za-z_-]*\d)[A-Za-z0-9_-]{32,}(?![\w/.-])/g, MASK)
  );
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
