import type { Entry } from '@hoplet/shared';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ANSWER_PAGES, lastAnswerMarkdown } from '../src/screens/cockpit/last-answer.ts';

const api = vi.hoisted(() => ({ fetchLines: vi.fn() }));
vi.mock('../src/lib/api.ts', async (original) => ({ ...(await original()), ...api }));

const PAGE = 80;
const user = (lineNo: number): Entry => ({
  lineNo,
  role: 'user',
  ts: '2026-10-05T09:00:00',
  blocks: [{ type: 'text', text: 'prompt' }],
});
const tool = (lineNo: number): Entry => ({
  lineNo,
  role: 'assistant',
  ts: '2026-10-05T09:00:00',
  blocks: [{ type: 'thinking', text: 'hmm' }],
});
const answer = (lineNo: number, text: string): Entry => ({
  lineNo,
  role: 'assistant',
  ts: '2026-10-05T09:00:00',
  blocks: [{ type: 'text', text }],
});

/** A session of `total` lines whose entries `at` gives; served as pages like the core does. */
function serve(total: number, at: (n: number) => Entry) {
  api.fetchLines.mockImplementation(async (id: string, start: number, limit: number) => {
    const from = start < 0 ? Math.max(0, total + start) : start;
    const end = Math.min(total, from + limit);
    const entries: Entry[] = [];
    for (let n = from; n < end; n++) entries.push(at(n));
    return { sessionId: id, start: from, end, total, entries };
  });
}

beforeEach(() => vi.resetAllMocks());

describe('lastAnswerMarkdown', () => {
  it('reads the tail page when it holds an answer', async () => {
    serve(100, (n) => (n === 90 ? answer(n, 'the answer') : user(n)));
    expect(await lastAnswerMarkdown('s')).toBe('the answer');
    expect(api.fetchLines).toHaveBeenCalledTimes(1);
  });

  it('pages backwards past lines that are not an answer', async () => {
    serve(300, (n) => (n === 100 ? answer(n, 'old answer') : n % 2 ? tool(n) : user(n)));
    expect(await lastAnswerMarkdown('s')).toBe('old answer');
    expect(api.fetchLines.mock.calls.map((c) => [c[1], c[2]])).toEqual([
      [-PAGE, PAGE],
      [140, PAGE],
      [60, PAGE],
    ]);
  });

  it('gives up after a bounded number of pages, and at the start of the session', async () => {
    serve(10_000, (n) => user(n));
    expect(await lastAnswerMarkdown('s')).toBeNull();
    expect(api.fetchLines).toHaveBeenCalledTimes(ANSWER_PAGES);
    api.fetchLines.mockClear();
    serve(30, (n) => tool(n));
    expect(await lastAnswerMarkdown('s')).toBeNull();
    expect(api.fetchLines).toHaveBeenCalledTimes(1);
  });
});
