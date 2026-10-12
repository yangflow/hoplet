/** Token classes of the `--color-syntax-*` tokens; `plain` text has no class. */
export type TokenClass = 'kw' | 'st' | 'cm' | 'nu' | 'ty' | 'fn' | 'pu' | 'h';
export type Token = { text: string; cls?: TokenClass };
export type Language = 'ts' | 'go' | 'py' | 'rust' | 'json' | 'sh' | 'md' | 'plain';

const KEYWORDS: Record<'ts' | 'go' | 'py' | 'rust' | 'sh', ReadonlySet<string>> = {
  ts: new Set(
    'import from type const let var export default async function await return try finally catch new if else for of in interface extends implements class throw switch case break continue while do typeof instanceof void null undefined true false this static readonly enum as yield delete'.split(
      ' ',
    ),
  ),
  go: new Set(
    'package import func return var const type struct interface map chan go defer if else for range switch case default break continue select nil true false fallthrough goto'.split(
      ' ',
    ),
  ),
  py: new Set(
    'import from def class return if elif else for while in not and or is None True False try except finally raise with as lambda pass yield async await global nonlocal assert del break continue'.split(
      ' ',
    ),
  ),
  rust: new Set(
    'use mod pub fn let mut const static struct enum impl trait for in while loop if else match return break continue where as ref self Self crate super move async await unsafe true false'.split(
      ' ',
    ),
  ),
  sh: new Set(
    'if then else elif fi for do done while case esac function in return export local set unset cd echo exit'.split(
      ' ',
    ),
  ),
};

const EXTENSIONS: Record<string, Language> = {
  ts: 'ts',
  tsx: 'ts',
  mts: 'ts',
  cts: 'ts',
  js: 'ts',
  jsx: 'ts',
  mjs: 'ts',
  cjs: 'ts',
  go: 'go',
  py: 'py',
  rs: 'rust',
  json: 'json',
  jsonc: 'json',
  ndjson: 'json',
  sh: 'sh',
  bash: 'sh',
  zsh: 'sh',
  md: 'md',
  mdx: 'md',
};

const FENCES: Record<string, Language> = {
  typescript: 'ts',
  tsx: 'ts',
  ts: 'ts',
  javascript: 'ts',
  js: 'ts',
  jsx: 'ts',
  go: 'go',
  golang: 'go',
  python: 'py',
  py: 'py',
  rust: 'rust',
  rs: 'rust',
  json: 'json',
  jsonc: 'json',
  sh: 'sh',
  bash: 'sh',
  shell: 'sh',
  zsh: 'sh',
  md: 'md',
  markdown: 'md',
};

/** The language of a file by its extension. */
export function languageOfPath(path: string): Language {
  const ext = path.split(/[/\\]/).pop()?.split('.').pop()?.toLowerCase() ?? '';
  return EXTENSIONS[ext] ?? 'plain';
}

/** The language a Markdown fence names. */
export function languageOfFence(info: string): Language {
  return FENCES[info.trim().split(/\s+/)[0]?.toLowerCase() ?? ''] ?? 'plain';
}

function push(out: Token[], text: string, cls?: TokenClass): void {
  if (!text) return;
  out.push(cls ? { text, cls } : { text });
}

function codeTokens(src: string, language: 'ts' | 'go' | 'py' | 'rust' | 'sh'): Token[] {
  const comment = language === 'py' || language === 'sh' ? '#.*$' : '\\/\\/.*$';
  const re = new RegExp(
    `(${comment})|("(?:[^"\\\\]|\\\\.)*"|'(?:[^'\\\\]|\\\\.)*'|\`(?:[^\`\\\\]|\\\\.)*\`)|(\\b\\d[\\d_.]*\\b)|([A-Za-z_$][\\w$]*)`,
    'g',
  );
  const keywords = KEYWORDS[language];
  const out: Token[] = [];
  let at = 0;
  for (let m = re.exec(src); m; m = re.exec(src)) {
    push(out, src.slice(at, m.index));
    at = re.lastIndex;
    const [text = '', cm, st, nu, id] = m;
    if (cm) push(out, text, 'cm');
    else if (st) push(out, text, 'st');
    else if (nu) push(out, text, 'nu');
    else if (id && keywords.has(id)) push(out, text, 'kw');
    else if (id && /^[A-Z]/.test(id)) push(out, text, 'ty');
    else if (id && src[at] === '(') push(out, text, 'fn');
    else push(out, text);
  }
  push(out, src.slice(at));
  return out;
}

function jsonTokens(src: string): Token[] {
  const re = /("(?:[^"\\]|\\.)*")(\s*:)?|(-?\b\d[\d.eE+-]*\b)|\b(true|false|null)\b/g;
  const out: Token[] = [];
  let at = 0;
  for (let m = re.exec(src); m; m = re.exec(src)) {
    push(out, src.slice(at, m.index));
    at = re.lastIndex;
    if (m[1]) {
      push(out, m[1], m[2] ? 'ty' : 'st');
      push(out, m[2] ?? '');
    } else if (m[3]) push(out, m[3], 'nu');
    else push(out, m[0], 'kw');
  }
  push(out, src.slice(at));
  return out;
}

function markdownTokens(src: string): Token[] {
  const heading = src.match(/^(#{1,6})(\s.*)$/);
  if (heading)
    return [
      { text: heading[1] ?? '', cls: 'kw' },
      { text: heading[2] ?? '', cls: 'h' },
    ];
  if (/^\|[\s|:-]+\|$/.test(src)) return [{ text: src, cls: 'pu' }];
  const re = /(\|)|(`[^`]*`)|(\b\d[\d,.]*\b)/g;
  const out: Token[] = [];
  let at = 0;
  for (let m = re.exec(src); m; m = re.exec(src)) {
    push(out, src.slice(at, m.index));
    at = re.lastIndex;
    push(out, m[0], m[1] ? 'pu' : m[2] ? 'st' : 'nu');
  }
  push(out, src.slice(at));
  return out;
}

/** Highlights one line of code; lines are independent, so a comment that spans lines is not carried over. */
export function highlightLine(src: string, language: Language): Token[] {
  if (src.length > 2000) return [{ text: src }];
  switch (language) {
    case 'plain':
      return [{ text: src }];
    case 'json':
      return jsonTokens(src);
    case 'md':
      return markdownTokens(src);
    default:
      return codeTokens(src, language);
  }
}
