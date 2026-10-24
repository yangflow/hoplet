// The message a review of diff comments becomes (#84): the same text for the preview and for the session.
import type { Annotation } from '@hoplet/shared';

/** Fence languages by file extension; any other file gets a plain fence. */
const FENCE_LANGUAGE: Record<string, string> = {
  ts: 'ts',
  tsx: 'tsx',
  mts: 'ts',
  cts: 'ts',
  js: 'js',
  jsx: 'jsx',
  mjs: 'js',
  cjs: 'js',
  go: 'go',
  py: 'python',
  rs: 'rust',
  rb: 'ruby',
  java: 'java',
  kt: 'kotlin',
  swift: 'swift',
  c: 'c',
  h: 'c',
  cc: 'cpp',
  cpp: 'cpp',
  hpp: 'cpp',
  cs: 'csharp',
  php: 'php',
  sh: 'sh',
  bash: 'bash',
  zsh: 'sh',
  sql: 'sql',
  json: 'json',
  yaml: 'yaml',
  yml: 'yaml',
  toml: 'toml',
  md: 'md',
  html: 'html',
  css: 'css',
  scss: 'scss',
};

function fenceLanguage(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1);
  const dot = name.lastIndexOf('.');
  return dot > 0 ? (FENCE_LANGUAGE[name.slice(dot + 1).toLowerCase()] ?? '') : '';
}

/** A fence longer than any run of backticks in the snippet, so the snippet cannot close it. */
function fenceFor(snippet: string): string {
  const longest = Math.max(0, ...(snippet.match(/`+/g) ?? []).map((run) => run.length));
  return '`'.repeat(Math.max(3, longest + 1));
}

/** Where a comment points: `path:42` or `path:42-44`, with the side when it is the file at `HEAD`. */
export function locationOf(a: Pick<Annotation, 'path' | 'side' | 'startLine' | 'endLine'>): string {
  const lines = a.startLine === a.endLine ? `${a.startLine}` : `${a.startLine}-${a.endLine}`;
  return `${a.path}:${lines}${a.side === 'old' ? ' (removed lines, as HEAD has them)' : ''}`;
}

/**
 * The review message: a heading with the count, the note, then each comment numbered with its location, the
 * lines it is about in a fence, and the comment.
 */
export function composeReview(comments: readonly Annotation[], note: string | undefined): string {
  const heading = `Review comments (${comments.length}) on the working tree.`;
  const items = comments.map((a, i) => {
    const parts = [`${i + 1}. ${locationOf(a)}`];
    if (a.snippet !== '') {
      const fence = fenceFor(a.snippet);
      parts.push(`${fence}${fenceLanguage(a.path)}\n${a.snippet}\n${fence}`);
    }
    parts.push(a.body);
    return parts.join('\n');
  });
  return [heading, ...(note ? [note] : []), ...items].join('\n\n');
}
