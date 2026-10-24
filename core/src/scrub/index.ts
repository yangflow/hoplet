/**
 * The one place that knows what a credential looks like. `scrubCredentials` masks the shapes that are
 * credentials wherever they appear (the log uses it on every line); `scrubText` also masks what a command or a
 * path names as a credential, which is too eager for prose (notification summaries use it); `redactForExport`
 * masks an exported session with markers that name each kind (ADR 0040).
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

/**
 * Whether `input` contains a credential by its shape (a key format, a bearer token, URL user info) or a private
 * key block. Text that merely names a credential (`TOKEN=value`) does not count: that is prose, and stored
 * content that must never hold a secret is checked with this.
 */
export function containsCredential(input: string): boolean {
  if (/-----BEGIN [A-Z ]*PRIVATE KEY-----/.test(input)) return true;
  return scrubCredentials(input, '\u0000') !== input;
}

/** What a masked value in an exported file says it was: `[redacted:<kind>]`. */
export type RedactionKind =
  | 'private-key'
  | 'url-credential'
  | 'bearer-token'
  | 'basic-auth'
  | 'query-credential'
  | 'api-key'
  | 'github-token'
  | 'hoplet-token'
  | 'slack-token'
  | 'aws-key'
  | 'jwt'
  | 'named-secret'
  | 'custom';

const MARKER = /(\[redacted:[a-z-]+\])/;

type ExportRule = {
  readonly kind: RedactionKind;
  readonly pattern: RegExp;
  /** The replacement given the marker; the marker alone when absent. */
  readonly to?: (marker: string) => (all: string, lead: string) => string;
};

const CREDENTIAL_NAME =
  '[A-Za-z0-9_.-]*(?:token|secret|passw(?:or)?d|passwd|api[_-]?key|apikey|auth|credential|private[_-]?key)[A-Za-z0-9_.-]*';

const keepLead = (m: string) => (_all: string, lead: string) => `${lead}${m}`;

// The rules of scrubText for an exported file, each with the kind its marker names, the shapes first so a value
// a credential name introduces (`HOPLET_TOKEN=hpt_…`) is named by its shape. There is no long-run rule: it
// would mask hashes, paths and identifiers all through a transcript. A credential name only counts before `=`
// or `:` (and `--name value`), never before a space in prose.
const EXPORT_RULES: readonly ExportRule[] = [
  {
    kind: 'private-key',
    pattern: /-----BEGIN ([A-Z ]*)PRIVATE KEY-----[\s\S]*?(?:-----END \1PRIVATE KEY-----|$)/g,
  },
  { kind: 'api-key', pattern: /\b(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}/g },
  { kind: 'github-token', pattern: /\b(?:ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]{16,}/g },
  { kind: 'hoplet-token', pattern: /\bhpt_[A-Za-z0-9_-]{16,}/g },
  { kind: 'slack-token', pattern: /\bxox[abprs]-[A-Za-z0-9-]{10,}/g },
  { kind: 'aws-key', pattern: /\bAKIA[0-9A-Z]{16}\b/g },
  { kind: 'jwt', pattern: /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*/g },
  {
    kind: 'url-credential',
    pattern: /(\b[a-z][a-z0-9+.-]*:\/\/)[^\s/@:]+:[^\s/@]+@/gi,
    to: (m) => (_all, scheme) => `${scheme}${m}@`,
  },
  { kind: 'bearer-token', pattern: /(\bBearer\s+)[^\s"'\\`]+/gi, to: keepLead },
  {
    kind: 'basic-auth',
    pattern: /\b(Basic|Token)\s+(?=[A-Za-z0-9._~+/=-]*\d)[A-Za-z0-9._~+/=-]{6,}/g,
    to: (m) => (_all, scheme) => `${scheme} ${m}`,
  },
  {
    kind: 'query-credential',
    pattern:
      /([?&](?:token|ticket|key|secret|password|sig|signature|access_token|api_key)=)[^&\s"'\\`]+/gi,
    to: keepLead,
  },
  {
    kind: 'named-secret',
    pattern: new RegExp(
      `(\\b${CREDENTIAL_NAME}["']?\\s*[=:]\\s*|--${CREDENTIAL_NAME}\\s+)(?!(?:Bearer|Basic|Token)\\s)(?:"[^"\\n]+"|'[^'\\n]+'|[^\\s"'\`,;()<>{}\\[\\]]{4,})`,
      'gi',
    ),
    to: (m) => (all, lead) => {
      const quote = all[lead.length] === '"' || all[lead.length] === "'" ? all[lead.length] : '';
      return `${lead}${quote}${m}${quote}`;
    },
  },
];

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** Runs `fn` on the parts of `text` outside the markers already written, so a masked value is never masked again. */
function outsideMarkers(text: string, fn: (part: string) => string): string {
  if (!text.includes('[redacted:')) return fn(text);
  return text
    .split(MARKER)
    .map((part, i) => (i % 2 === 1 ? part : fn(part)))
    .join('');
}

/**
 * Masks, for a file a person will share, what scrubText would (without its long-run rule) and private key blocks,
 * as `[redacted:<kind>]`, then each of `extra` (matched without regard to case) as `[redacted:custom]`. Best
 * effort: a secret of no known shape stays. Returns the text and how many values it masked.
 */
export function redactForExport(
  input: string,
  extra: readonly string[] = [],
): { text: string; masked: number } {
  let masked = 0;
  let out = input;
  const rules: ExportRule[] = [...EXPORT_RULES];
  const terms = extra.filter((s) => s.length > 0);
  if (terms.length > 0) {
    // The longest first, so a term inside another does not split it.
    const sorted = [...terms].sort((a, b) => b.length - a.length).map(escapeRegExp);
    rules.push({ kind: 'custom', pattern: new RegExp(sorted.join('|'), 'gi') });
  }
  for (const rule of rules) {
    const marker = `[redacted:${rule.kind}]`;
    const to = rule.to?.(marker);
    out = outsideMarkers(out, (part) =>
      part.replace(rule.pattern, (all: string, lead: unknown) => {
        masked++;
        return to ? to(all, typeof lead === 'string' ? lead : '') : marker;
      }),
    );
  }
  return { text: out, masked };
}
