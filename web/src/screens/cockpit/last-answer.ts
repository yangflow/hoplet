import type { Entry } from '@hoplet/shared';
import { fetchLines } from '../../lib/api.ts';
import { loadBlock } from './conversation/blocks.tsx';
import { PAGE_LINES } from './conversation/page-cache.ts';
import { buildTurns, type TextRow } from './conversation/turns.ts';

/** The last assistant text of the entries: the newest turn that has one, its last text row. */
export function lastAnswerRow(entries: readonly Entry[]): TextRow | null {
  const turns = buildTurns(entries);
  for (let t = turns.length - 1; t >= 0; t--) {
    const rows = turns[t]?.rows ?? [];
    for (let r = rows.length - 1; r >= 0; r--) {
      const row = rows[r];
      if (row?.kind === 'text' && row.text.trim()) return row;
    }
  }
  return null;
}

/** How many pages back from the tail the last answer is looked for. */
export const ANSWER_PAGES = 5;

/**
 * The last answer of a session as Markdown: the text of its newest assistant message (tool results and thinking
 * are not answers), read from the tail of the session a page at a time, at most `ANSWER_PAGES` pages back; an
 * elided text is fetched whole. `null` when those pages hold no answer.
 */
export async function lastAnswerMarkdown(sessionId: string): Promise<string | null> {
  let start = -PAGE_LINES;
  let limit = PAGE_LINES;
  for (let page = 0; page < ANSWER_PAGES; page++) {
    const lines = await fetchLines(sessionId, start, limit);
    const row = lastAnswerRow(lines.entries);
    if (row) {
      const text = row.elided
        ? await loadBlock(sessionId, row.elided.line, row.elided.block)
        : row.text;
      return text.trim() || null;
    }
    if (lines.start <= 0) return null;
    start = Math.max(0, lines.start - PAGE_LINES);
    limit = lines.start - start;
  }
  return null;
}
