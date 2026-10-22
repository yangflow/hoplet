/**
 * The one place that knows what a credential looks like. `scrubCredentials` masks the shapes that are
 * credentials wherever they appear (the log uses it on every line); `scrubText` also masks what a command or a
 * path names as a credential, which is too eager for prose (notification summaries use it).
 */

/** The mask a notification summary carries. */
export const TEXT_MASK = '***';

type Replacer = string | ((substring: string, ...args: string[]) => string);
type Rule = {
  readonly pattern: RegExp;
  readonly text?: true;
  readonly to: (mask: string) => Replacer;
};

// Applied in this order. A rule marked `text` is only for scrubText.
const RULES: readonly Rule[] = [
  // user:password@ in a URL
  { pattern: /(\b[a-z][a-z0-9+.-]*:\/\/)[^\s/@:]+:[^\s/@]+@/gi, to: (m) => `$1${m}@` },
  // Authorization header credentials
  { pattern: /(\bBearer\s+)[^\s"'\\]+/gi, to: (m) => `$1${m}` },
  { pattern: /\b(Basic|Token)\s+[A-Za-z0-9._~+/=-]{6,}/gi, text: true, to: (m) => `$1 ${m}` },
  // NAME=value and --name value where the name says it is a credential
  {
    pattern:
      /\b([A-Za-z0-9_.-]*(?:token|secret|passw(?:or)?d|passwd|api[_-]?key|apikey|auth|credential|private[_-]?key)[A-Za-z0-9_.-]*)(\s*[=:]\s*|\s+)(?:"[^"]*"|'[^']*'|\S+)/gi,
    text: true,
    to: (m) => (_all, name, sep) => `${name}${(sep ?? '').trim() === '' ? ' ' : sep}${m}`,
  },
  // query-string credentials
  {
    pattern:
      /([?&](?:token|ticket|key|secret|password|sig|signature|access_token|api_key)=)[^&\s"'\\]+/gi,
    to: (m) => `$1${m}`,
  },
  // well-known key shapes
  { pattern: /\b(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}/g, to: (m) => m },
  { pattern: /\b(?:ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]{16,}/g, to: (m) => m },
  { pattern: /\bhpt_[A-Za-z0-9_-]{16,}/g, to: (m) => m },
  { pattern: /\bxox[abprs]-[A-Za-z0-9-]{10,}/g, to: (m) => m },
  { pattern: /\bAKIA[0-9A-Z]{16}\b/g, to: (m) => m },
  { pattern: /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*/g, to: (m) => m },
  // a long unbroken run of key-like characters with a digit in it (a path segment follows a slash and is kept)
  {
    pattern: /(?<![\w/.-])(?=[A-Za-z_-]*\d)[A-Za-z0-9_-]{32,}(?![\w/.-])/g,
    text: true,
    to: (m) => m,
  },
];

function apply(input: string, mask: string, withText: boolean): string {
  let out = input;
  for (const rule of RULES) {
    if (rule.text && !withText) continue;
    out = out.replace(rule.pattern, rule.to(mask) as string);
  }
  return out;
}

/** Masks credential shapes: URL user info, bearer credentials, credential query parameters, key formats. */
export function scrubCredentials(input: string, mask: string): string {
  return apply(input, mask, false);
}

/**
 * Masks everything `scrubCredentials` does, plus values a command line or path names as a credential
 * (`NAME=value`, `--name value`, `Basic`/`Token` headers, long key-like runs). The result is for people,
 * not for running.
 */
export function scrubText(input: string, mask: string = TEXT_MASK): string {
  return apply(input, mask, true);
}
