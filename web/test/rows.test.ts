// @vitest-environment jsdom
import type { Entry, ToolKind } from '@hoplet/shared';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { RowView } from '../src/screens/cockpit/rows.tsx';
import { buildTurns } from '../src/screens/cockpit/turns.ts';
import { ViewContext, type ViewState } from '../src/screens/cockpit/view-context.tsx';

let root: Root;
let host: HTMLDivElement;
const opened = new Map<string, boolean>();
const view: ViewState = {
  sessionId: 's',
  isOpen: (key, byDefault = false) => opened.get(key) ?? byDefault,
  setOpen: (key, open) => void opened.set(key, open),
};

const user = (lineNo: number): Entry => ({
  lineNo,
  role: 'user',
  blocks: [{ type: 'text', text: 'go' }],
});
/** The kinds the core's mapping gives these tools. */
const KINDS: Record<string, ToolKind> = {
  Bash: 'run',
  Read: 'read',
  Edit: 'edit',
  Write: 'write',
  Grep: 'search',
  WebFetch: 'fetch',
};
const call = (lineNo: number, id: string, name: string, input: unknown, ts: string): Entry => ({
  lineNo,
  role: 'assistant',
  ts,
  blocks: [
    name.startsWith('mcp__')
      ? {
          type: 'tool-call',
          id,
          name,
          kind: 'call',
          mcp: { server: 'github', tool: 'list_prs' },
          input,
        }
      : { type: 'tool-call', id, name, kind: KINDS[name] ?? 'other', input },
  ],
});
const result = (
  lineNo: number,
  id: string,
  content: string,
  ts: string,
  isError = false,
): Entry => ({
  lineNo,
  role: 'user',
  ts,
  blocks: [{ type: 'tool-result', id, content, ...(isError ? { isError } : {}) }],
});

async function render(entries: Entry[]) {
  const [turn] = buildTurns([user(0), ...entries], { cwd: '/repo' });
  await act(async () =>
    root.render(
      createElement(
        ViewContext,
        { value: view },
        ...(turn?.rows ?? []).map((row) => createElement(RowView, { key: row.key, row })),
      ),
    ),
  );
}
const cells = (row: Element) =>
  [...row.querySelectorAll(':scope > span')].map((c) => c.textContent);

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  Object.defineProperty(navigator, 'language', { configurable: true, value: 'en-US' });
  opened.clear();
  host = document.createElement('div');
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

describe('tool rows', () => {
  it('shows one row of verb, object and meta, and opens its output on demand', async () => {
    await render([
      call(1, 'a', 'Read', { file_path: '/repo/core/a.ts' }, '2026-10-05T09:00:00.000'),
      result(2, 'a', '     1→one\n     2→two', '2026-10-05T09:00:00.100'),
    ]);
    const row = host.querySelector('[data-testid="cockpit.activity"] summary.act') as Element;
    expect(cells(row)).toEqual(['', 'Read', 'core/a.ts', 'L1–2·21 B']);
    expect(host.querySelector('.well')).toBeNull();
    const details = row.closest('details') as HTMLDetailsElement;
    details.open = true;
    await act(async () => details.dispatchEvent(new Event('toggle')));
    expect(opened.get('1-b0#a')).toBe(true);
  });

  it('shows the command line and output of a run in a numbered-less well when opened', async () => {
    opened.set('1-b0#a', true);
    await render([
      call(1, 'a', 'Bash', { command: 'ls core' }, '2026-10-05T09:00:00.000'),
      result(2, 'a', 'src', '2026-10-05T09:00:00.100'),
    ]);
    const row = host.querySelector('summary.act') as Element;
    expect(cells(row)).toEqual(['', 'Ran', 'ls core', 'exit 0·0.1 s']);
    expect([...host.querySelectorAll('.well .wl')].map((l) => l.textContent)).toEqual([
      'ls core',
      'src',
    ]);
    expect(host.querySelector('.well .wl.cmd')).not.toBeNull();
    expect(host.querySelector('.well.ln')).toBeNull();
  });

  it('keeps a failed row in place with the danger rule and its error open', async () => {
    await render([
      call(1, 'a', 'Bash', { command: 'false' }, '2026-10-05T09:00:00.000'),
      result(2, 'a', 'Exit code 1\nboom', '2026-10-05T09:00:42.000', true),
    ]);
    const row = host.querySelector('summary.act') as Element;
    expect(row.classList.contains('bad')).toBe(true);
    expect(cells(row)[3]).toBe('exit 1·42 s');
    expect([...host.querySelectorAll('.well.err .wl')].map((l) => l.textContent)).toEqual([
      'false',
      'boom',
    ]);
  });

  it('numbers a written file and names a search hit by path and line', async () => {
    opened.set('1-b0#w', true);
    opened.set('3-b0#g', true);
    await render([
      call(
        1,
        'w',
        'Write',
        { file_path: '/repo/a.ts', content: 'x\ny\n' },
        '2026-10-05T09:00:00.000',
      ),
      result(2, 'w', 'created', '2026-10-05T09:00:00.100'),
      call(3, 'g', 'Grep', { pattern: 'foo', output_mode: 'content' }, '2026-10-05T09:00:01.000'),
      result(4, 'g', 'a.ts:1:let foo = 1\nb.ts:9:foo()', '2026-10-05T09:00:01.100'),
    ]);
    const rows = [...host.querySelectorAll('summary.act')].map(cells);
    expect(rows).toEqual([
      ['', 'Wrote', 'a.ts', '2 lines·4 B'],
      ['', 'Searched', '"foo"', '2 matches'],
    ]);
    expect(host.querySelectorAll('.well.ln .wl .n')).toHaveLength(2);
    expect([...host.querySelectorAll('.well:not(.ln) .wl')].map((l) => l.textContent)).toEqual([
      'a.ts:1  let foo = 1',
      'b.ts:9  foo()',
    ]);
    expect(host.querySelector('.well .wl .hl')?.textContent).toBe('foo');
  });

  it('folds three calls into one row naming their verbs and opens it into the rows', async () => {
    await render([
      call(1, 'a', 'Bash', { command: 'ls' }, '2026-10-05T09:00:00.000'),
      call(2, 'b', 'mcp__github__list_prs', {}, '2026-10-05T09:00:01.000'),
      call(3, 'c', 'WebFetch', { url: 'https://example.com/x' }, '2026-10-05T09:00:02.000'),
    ]);
    const fold = host.querySelector('[data-testid="cockpit.tools"] > summary.act') as Element;
    expect(cells(fold).slice(1, 3)).toEqual(['3 tools', 'Ran, Called, Fetched']);
    opened.set('fold-1-b0#a', true);
    await render([
      call(1, 'a', 'Bash', { command: 'ls' }, '2026-10-05T09:00:00.000'),
      call(2, 'b', 'mcp__github__list_prs', {}, '2026-10-05T09:00:01.000'),
      call(3, 'c', 'WebFetch', { url: 'https://example.com/x' }, '2026-10-05T09:00:02.000'),
    ]);
    expect(
      [...host.querySelectorAll('[data-testid="cockpit.activity"] .act')].map((r) =>
        cells(r).slice(1, 3),
      ),
    ).toEqual([
      ['Ran', 'ls'],
      ['Called', 'github › list_prs'],
      ['Fetched', 'example.com'],
    ]);
  });

  it('shows an edit with its counts and its diff in the well', async () => {
    await render([
      call(
        1,
        'e',
        'Edit',
        { file_path: '/repo/a.ts', old_string: 'a\nb', new_string: 'a\nB\nc' },
        '2026-10-05T09:00:00.000',
      ),
    ]);
    const row = host.querySelector('summary.act') as Element;
    expect(cells(row)).toEqual(['', 'Edited', 'a.ts', '+2−1']);
    expect(host.querySelectorAll('.well .dl')).toHaveLength(4);
    expect(host.querySelector('[data-testid="cockpit.open-diff"]')).not.toBeNull();
  });
});
