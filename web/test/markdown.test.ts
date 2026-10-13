import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { lineDiff } from '../src/screens/cockpit/conversation/diff.ts';
import {
  highlightLine,
  languageOfFence,
  languageOfPath,
} from '../src/screens/cockpit/conversation/highlight.ts';
import { Markdown, parseMarkdown } from '../src/screens/cockpit/conversation/markdown.tsx';

const html = (text: string, caret = false) =>
  renderToStaticMarkup(createElement(Markdown, { text, caret }));

describe('Markdown', () => {
  it('shows raw HTML as text', () => {
    const out = html('<img src=x onerror=alert(1)> and <script>alert(2)</script>');
    expect(out).not.toContain('<img');
    expect(out).not.toContain('<script');
    expect(out).toContain('&lt;img src=x onerror=alert(1)&gt;');
  });

  it('links only http, https and mailto', () => {
    const out = html(
      '[ok](https://example.com) [mail](mailto:a@b.c) [bad](javascript:alert(1)) [data](data:text/html,x)',
    );
    expect(out).toContain('href="https://example.com"');
    expect(out).toContain('rel="noopener noreferrer"');
    expect(out).toContain('href="mailto:a@b.c"');
    expect(out).not.toContain('javascript:');
    expect(out).not.toContain('data:text');
    expect(out).toContain('bad');
  });

  it('renders headings, lists, quotes, tables and inline marks', () => {
    const out = html(
      '# Title\n\n- one\n- **two** and `three`\n  - nested\n\n> quoted\n\n| a | b |\n| - | - |\n| 1 | 2 |\n\n*em*',
    );
    expect(out).toContain('<h3>Title</h3>');
    expect(out).toContain('<strong>two</strong>');
    expect(out).toContain('<code>three</code>');
    expect(out).toContain('<blockquote>');
    expect(out).toContain('<th>a</th>');
    expect(out).toContain('<em>em</em>');
    expect(out.match(/<ul>/g)).toHaveLength(2);
  });

  it('highlights fenced code and keeps its text verbatim', () => {
    const blocks = parseMarkdown('```ts\nconst a = "x"; // note\n```');
    expect(blocks).toEqual([{ type: 'code', lang: 'ts', text: 'const a = "x"; // note' }]);
    const out = html('```ts\nconst a = "x"; // note\n```');
    expect(out).toContain('<span class="sx-kw">const</span>');
    expect(out).toContain('<span class="sx-st">&quot;x&quot;</span>');
    expect(out).toContain('<span class="sx-cm">// note</span>');
  });

  it('puts the caret of streamed text at the end of its last paragraph', () => {
    const out = html('first\n\nsecond', true);
    expect(out.match(/class="caret"/g)).toHaveLength(1);
    expect(out).toContain('second<span class="caret"');
  });
});

describe('highlight', () => {
  it('names the language of a file and of a fence', () => {
    expect(languageOfPath('web/src/a.tsx')).toBe('ts');
    expect(languageOfPath('notes/x.md')).toBe('md');
    expect(languageOfPath('Makefile')).toBe('plain');
    expect(languageOfFence('golang')).toBe('go');
  });

  it('colours keywords, types, functions and numbers', () => {
    const tokens = highlightLine('export function toItems(e: Entry): Item[] { return 42; }', 'ts');
    const byClass = (cls: string) => tokens.filter((t) => t.cls === cls).map((t) => t.text);
    expect(byClass('kw')).toEqual(['export', 'function', 'return']);
    expect(byClass('fn')).toEqual(['toItems']);
    expect(byClass('ty')).toEqual(['Entry', 'Item']);
    expect(byClass('nu')).toEqual(['42']);
    expect(tokens.map((t) => t.text).join('')).toBe(
      'export function toItems(e: Entry): Item[] { return 42; }',
    );
  });
});

describe('lineDiff', () => {
  it('shows the changed lines with context and counts them', () => {
    const d = lineDiff('a\nb\nc\nd\ne', 'a\nb\nX\nY\nd\ne');
    expect([d.added, d.removed]).toEqual([2, 1]);
    expect(d.rows.map((r) => `${r.kind}:${r.text}`)).toEqual([
      'eq:a',
      'eq:b',
      'del:c',
      'add:X',
      'add:Y',
      'eq:d',
      'eq:e',
    ]);
    expect(d.rows.find((r) => r.kind === 'add')).toMatchObject({ newNo: 3 });
    expect(d.rows.find((r) => r.kind === 'del')).toMatchObject({ oldNo: 3 });
  });

  it('finds an unchanged line between two changes', () => {
    const d = lineDiff('a\nkeep\nb', 'A\nkeep\nB');
    expect(d.rows.map((r) => r.kind)).toEqual(['del', 'add', 'eq', 'del', 'add']);
  });

  it('treats a new file as all additions and caps the rows', () => {
    const text = Array.from({ length: 200 }, (_, i) => `l${i}`).join('\n');
    const d = lineDiff('', text);
    expect([d.added, d.removed]).toEqual([200, 0]);
    expect(d.rows).toHaveLength(80);
    expect(d.more).toBe(120);
  });
});
