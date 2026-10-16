/**
 * Safe Markdown for assistant prose: headings, paragraphs, lists, quotes, tables, code, inline code,
 * emphasis and links. It builds React elements only; raw HTML in the text is shown as text, and only http,
 * https and mailto links are links.
 */
import type { ReactNode } from 'react';
import { CodeLines } from './code.tsx';
import { type Language, languageOfFence } from './highlight.ts';

export type MdBlock =
  | { type: 'heading'; level: number; text: string }
  | { type: 'paragraph'; text: string; caret?: boolean }
  | { type: 'code'; lang: Language; text: string }
  | { type: 'quote'; blocks: MdBlock[] }
  | { type: 'list'; ordered: boolean; items: { blocks: MdBlock[] }[] }
  | { type: 'table'; head: string[]; rows: string[][] }
  | { type: 'rule' };

const FENCE = /^\s{0,3}(`{3,}|~{3,})\s*([^`\s]*)[^`]*$/;
const BULLET = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;

function cells(line: string): string[] {
  return line
    .trim()
    .replace(/^\||\|$/g, '')
    .split('|')
    .map((c) => c.trim());
}

const indentOf = (line: string) => line.length - line.trimStart().length;
const isRule = (line: string) => /^\s{0,3}([-*_])(\s*\1){2,}\s*$/.test(line);
const isSeparator = (line: string) =>
  line.includes('|') && /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/.test(line);

/** Splits the source into blocks. */
export function parseMarkdown(src: string): MdBlock[] {
  const lines = src.replace(/\r\n?/g, '\n').split('\n');
  const out: MdBlock[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i] ?? '';
    if (line.trim() === '') {
      i++;
      continue;
    }
    const fence = FENCE.exec(line);
    if (fence) {
      const mark = (fence[1] ?? '```')[0] ?? '`';
      const size = (fence[1] ?? '').length;
      const body: string[] = [];
      i++;
      while (
        i < lines.length &&
        !new RegExp(`^\\s{0,3}${mark === '`' ? '`' : '~'}{${size},}\\s*$`).test(lines[i] ?? '')
      ) {
        body.push(lines[i] ?? '');
        i++;
      }
      i++;
      out.push({ type: 'code', lang: languageOfFence(fence[2] ?? ''), text: body.join('\n') });
      continue;
    }
    const heading = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line);
    if (heading) {
      out.push({ type: 'heading', level: (heading[1] ?? '#').length, text: heading[2] ?? '' });
      i++;
      continue;
    }
    if (isRule(line)) {
      out.push({ type: 'rule' });
      i++;
      continue;
    }
    if (/^\s{0,3}>/.test(line)) {
      const body: string[] = [];
      while (i < lines.length && /^\s{0,3}>/.test(lines[i] ?? '')) {
        body.push((lines[i] ?? '').replace(/^\s{0,3}>\s?/, ''));
        i++;
      }
      out.push({ type: 'quote', blocks: parseMarkdown(body.join('\n')) });
      continue;
    }
    if (
      line.includes('|') &&
      i + 1 < lines.length &&
      isSeparator(lines[i + 1] ?? '') &&
      (lines[i + 1] ?? '').includes('-')
    ) {
      const head = cells(line);
      const rows: string[][] = [];
      i += 2;
      while (i < lines.length && (lines[i] ?? '').trim() !== '' && (lines[i] ?? '').includes('|')) {
        rows.push(cells(lines[i] ?? ''));
        i++;
      }
      out.push({ type: 'table', head, rows });
      continue;
    }
    const bullet = BULLET.exec(line);
    if (bullet) {
      const ordered = /\d/.test(bullet[2] ?? '');
      const base = (bullet[1] ?? '').length;
      const items: { blocks: MdBlock[] }[] = [];
      while (i < lines.length) {
        const m = BULLET.exec(lines[i] ?? '');
        if (!m || (m[1] ?? '').length !== base || /\d/.test(m[2] ?? '') !== ordered) break;
        const body = [m[3] ?? ''];
        i++;
        for (; i < lines.length; i++) {
          const next = lines[i] ?? '';
          if (next.trim() === '') {
            const after = lines[i + 1] ?? '';
            if (after.trim() !== '' && indentOf(after) > base) {
              body.push('');
              continue;
            }
            break;
          }
          if (indentOf(next) <= base) break;
          body.push(next.slice(Math.min(indentOf(next), base + 2)));
        }
        items.push({ blocks: parseMarkdown(body.join('\n')) });
      }
      out.push({ type: 'list', ordered, items });
      continue;
    }
    const para: string[] = [];
    while (i < lines.length) {
      const next = lines[i] ?? '';
      if (
        next.trim() === '' ||
        FENCE.test(next) ||
        /^\s{0,3}#{1,6}\s/.test(next) ||
        /^\s{0,3}>/.test(next) ||
        isRule(next)
      )
        break;
      if (para.length > 0 && BULLET.test(next)) break;
      para.push(next.trim());
      i++;
    }
    out.push({ type: 'paragraph', text: para.join('\n') });
  }
  return out;
}

const SAFE_URL = /^(https?:\/\/|mailto:)/i;

const INLINE =
  /(`+)([^`]|[^`][\s\S]*?[^`])\1(?!`)|\*\*([^*\n][\s\S]*?)\*\*|\*([^*\s][^*\n]*?)\*|\[([^\]\n]+)\]\(([^)\s]+)\)/g;

/** Inline Markdown as elements. */
export function renderInline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  let at = 0;
  let n = 0;
  // A fresh expression per call: the recursion below must not share its position.
  const re = new RegExp(INLINE.source, 'g');
  for (let m = re.exec(text); m; m = re.exec(text)) {
    if (m.index > at) out.push(text.slice(at, m.index));
    at = re.lastIndex;
    const key = n++;
    if (m[2] !== undefined) out.push(<code key={key}>{m[2].trim()}</code>);
    else if (m[3] !== undefined) out.push(<strong key={key}>{renderInline(m[3])}</strong>);
    else if (m[4] !== undefined) out.push(<em key={key}>{renderInline(m[4])}</em>);
    else if (m[5] !== undefined && m[6] !== undefined) {
      if (SAFE_URL.test(m[6])) {
        out.push(
          <a key={key} href={m[6]} target="_blank" rel="noopener noreferrer">
            {renderInline(m[5])}
          </a>,
        );
      } else out.push(m[5]);
    }
  }
  if (at < text.length) out.push(text.slice(at));
  return out;
}

function Blocks({ blocks, base }: { blocks: MdBlock[]; base: number }): ReactNode {
  return blocks.map((block, i) => {
    const key = `${block.type}${i}`;
    switch (block.type) {
      case 'heading': {
        const Tag = `h${Math.min(6, Math.max(2, block.level + base))}` as 'h3';
        return <Tag key={key}>{renderInline(block.text)}</Tag>;
      }
      case 'paragraph':
        return (
          <p key={key}>
            {renderInline(block.text)}
            {block.caret ? <span className="caret" aria-hidden="true" /> : null}
          </p>
        );
      case 'code':
        return (
          <pre key={key} className="md-code">
            <CodeLines text={block.text} language={block.lang} />
          </pre>
        );
      case 'quote':
        return (
          <blockquote key={key}>
            <Blocks blocks={block.blocks} base={base} />
          </blockquote>
        );
      case 'list': {
        const Tag = block.ordered ? 'ol' : 'ul';
        return (
          <Tag key={key}>
            {block.items.map((item, j) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: list items have no identity
              <li key={j}>
                <Blocks blocks={item.blocks} base={base} />
              </li>
            ))}
          </Tag>
        );
      }
      case 'table':
        return (
          <div key={key} className="md-table">
            <table>
              <thead>
                <tr>
                  {block.head.map((c, j) => (
                    // biome-ignore lint/suspicious/noArrayIndexKey: cells have no identity
                    <th key={j}>{renderInline(c)}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {block.rows.map((row, r) => (
                  // biome-ignore lint/suspicious/noArrayIndexKey: rows have no identity
                  <tr key={r}>
                    {row.map((c, j) => (
                      // biome-ignore lint/suspicious/noArrayIndexKey: cells have no identity
                      <td key={j}>{renderInline(c)}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        );
      case 'rule':
        return <hr key={key} />;
      default:
        return null;
    }
  });
}

/** Puts the caret of streamed text at the end of its last paragraph, or after the last block when it is not one. */
function withCaret(blocks: MdBlock[]): { blocks: MdBlock[]; placed: boolean } {
  const last = blocks[blocks.length - 1];
  if (!last) return { blocks, placed: false };
  const head = blocks.slice(0, -1);
  if (last.type === 'paragraph')
    return { blocks: [...head, { ...last, caret: true }], placed: true };
  if (last.type === 'list') {
    const item = last.items[last.items.length - 1];
    if (item) {
      const inner = withCaret(item.blocks);
      if (inner.placed) {
        const items = [...last.items.slice(0, -1), { blocks: inner.blocks }];
        return { blocks: [...head, { ...last, items }], placed: true };
      }
    }
  }
  return { blocks, placed: false };
}

/** Assistant prose: sans 15/1.6 within 68ch (docs/design.md#typography); `caret` marks streamed text. */
/** `base` is how many levels a `#` heading sits below the page's `h1`: 2 in a turn, 0 in a plan card (headings never go above `h2`). */
export function Markdown({
  text,
  caret,
  base = 2,
}: {
  text: string;
  caret?: boolean;
  base?: number;
}) {
  const parsed = parseMarkdown(text);
  const { blocks, placed } = caret ? withCaret(parsed) : { blocks: parsed, placed: true };
  return (
    <div className="md">
      <Blocks blocks={blocks} base={base} />
      {caret && !placed ? <span className="caret" aria-hidden="true" /> : null}
    </div>
  );
}
