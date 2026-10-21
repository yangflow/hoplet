/**
 * Placeholders of a session template (docs/spec.md#session-templates). The core resolves them when a template
 * is saved and when it starts a session; the web UI uses the same functions for its form and its preview.
 */

export const MAX_TEMPLATE_TEXT_CHARS = 8_000;
export const MAX_PLACEHOLDERS = 20;
export const MAX_PLACEHOLDER_NAME_CHARS = 32;
export const MAX_PLACEHOLDER_DEFAULT_CHARS = 200;

/** A placeholder in the order of its first use; the first default counts. */
export interface Placeholder {
  name: string;
  default?: string;
}

/** A piece of the text: literal text (escapes already resolved) or a use of a placeholder. */
export type TemplatePart = { kind: 'text'; text: string } | { kind: 'field'; name: string };

export type PlaceholderProblem = 'syntax' | 'too-many';

export type ParsedTemplate =
  | { ok: true; parts: TemplatePart[]; placeholders: Placeholder[] }
  | { ok: false; problem: PlaceholderProblem; position: number };

const NAME = /^[a-z][a-z0-9_]*/;

/**
 * Splits `text` into literal parts and placeholders. `\{{` is a literal `{{`; every other backslash stays. A `{{`
 * that does not start a valid placeholder is refused with its position (a 0-based offset into `text`), and so is a
 * 21st distinct placeholder.
 */
export function parseTemplate(text: string): ParsedTemplate {
  const parts: TemplatePart[] = [];
  const placeholders: Placeholder[] = [];
  let literal = '';
  const flush = (): void => {
    if (literal !== '') parts.push({ kind: 'text', text: literal });
    literal = '';
  };
  let i = 0;
  while (i < text.length) {
    if (text.startsWith('\\{{', i)) {
      literal += '{{';
      i += 3;
      continue;
    }
    if (!text.startsWith('{{', i)) {
      literal += text[i];
      i += 1;
      continue;
    }
    const start = i;
    const name = NAME.exec(text.slice(i + 2))?.[0];
    if (name === undefined || name.length > MAX_PLACEHOLDER_NAME_CHARS) {
      return { ok: false, problem: 'syntax', position: start };
    }
    let after = i + 2 + name.length;
    let fallback: string | undefined;
    if (text.startsWith('}}', after)) {
      after += 2;
    } else if (text[after] === ':') {
      const close = text.indexOf('}}', after + 1);
      if (close < 0) return { ok: false, problem: 'syntax', position: start };
      fallback = text.slice(after + 1, close);
      if (/[\r\n]/.test(fallback) || fallback.length > MAX_PLACEHOLDER_DEFAULT_CHARS) {
        return { ok: false, problem: 'syntax', position: start };
      }
      after = close + 2;
    } else {
      return { ok: false, problem: 'syntax', position: start };
    }
    if (!placeholders.some((p) => p.name === name)) {
      if (placeholders.length >= MAX_PLACEHOLDERS) {
        return { ok: false, problem: 'too-many', position: start };
      }
      placeholders.push(fallback === undefined ? { name } : { name, default: fallback });
    }
    flush();
    parts.push({ kind: 'field', name });
    i = after;
  }
  flush();
  return { ok: true, parts, placeholders };
}

export type FilledTemplate =
  | { ok: true; text: string }
  | { ok: false; missing: string[]; unknown: string[] };

/**
 * The prompt with `values` inserted as typed; a value is never scanned again. A placeholder with no value uses its
 * default; a placeholder with neither, or an empty value and no default, is `missing`. A name in `values` that the
 * template does not have is `unknown`. The caller checks the length of the result.
 */
export function fillTemplate(
  parsed: Extract<ParsedTemplate, { ok: true }>,
  values: Readonly<Record<string, string>>,
): FilledTemplate {
  const known = new Set(parsed.placeholders.map((p) => p.name));
  const unknown = Object.keys(values).filter((name) => !known.has(name));
  const missing: string[] = [];
  const chosen = new Map<string, string>();
  for (const p of parsed.placeholders) {
    const given = Object.hasOwn(values, p.name) ? values[p.name] : undefined;
    const value =
      given === undefined || (given === '' && p.default === undefined) ? p.default : given;
    if (value === undefined) missing.push(p.name);
    else chosen.set(p.name, value);
  }
  if (missing.length > 0 || unknown.length > 0) return { ok: false, missing, unknown };
  let text = '';
  for (const part of parsed.parts) {
    text += part.kind === 'text' ? part.text : (chosen.get(part.name) ?? '');
  }
  return { ok: true, text };
}
