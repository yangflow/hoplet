import { readFileSync } from 'node:fs';
import path from 'node:path';
import type { Entry, ToolKind } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  buildTurns,
  type EditRow,
  formatTokens,
  parseCommand,
  stripAnsi,
  type ToolRow,
} from '../src/screens/cockpit/conversation/turns.ts';

const ts = (s: string) => `2026-10-05T09:${s}`;
const user = (lineNo: number, text: string, at = '00:00'): Entry => ({
  lineNo,
  role: 'user',
  ts: ts(at),
  blocks: [{ type: 'text', text }],
});
const say = (lineNo: number, text: string, at = '00:05'): Entry => ({
  lineNo,
  role: 'assistant',
  ts: ts(at),
  blocks: [{ type: 'text', text }],
});
/** The kinds the core's mapping gives these tools. */
const KINDS: Record<string, ToolKind> = {
  Bash: 'run',
  Read: 'read',
  Edit: 'edit',
  Write: 'write',
  Grep: 'search',
  WebFetch: 'fetch',
  LS: 'list',
};
const call = (lineNo: number, id: string, name: string, input: unknown, at = '00:10'): Entry => ({
  lineNo,
  role: 'assistant',
  ts: ts(at),
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
const result = (lineNo: number, id: string, content: string, isError = false): Entry => ({
  lineNo,
  role: 'user',
  ts: ts('00:11'),
  blocks: [{ type: 'tool-result', id, content, ...(isError ? { isError } : {}) }],
});

describe('buildTurns', () => {
  it('starts a turn at each user prompt and attaches the rest to it', () => {
    const turns = buildTurns([
      user(0, 'first'),
      say(1, 'answer'),
      call(2, 't1', 'Bash', { command: 'ls' }),
      result(3, 't1', 'a b'),
      user(4, 'second', '02:00'),
      say(5, 'done', '02:30'),
    ]);
    expect(turns.map((t) => [t.key, t.startLine, t.endLine, t.prompt?.text])).toEqual([
      ['t0', 0, 3, 'first'],
      ['t4', 4, 5, 'second'],
    ]);
    expect(turns[0]?.rows.map((r) => r.kind)).toEqual(['text', 'tool']);
  });

  it('treats lines before the first prompt as a turn without one', () => {
    const turns = buildTurns([say(7, 'tail of an earlier turn'), user(8, 'next')]);
    expect(turns[0]?.prompt).toBeNull();
    expect(turns[0]?.key).toBe('h7');
    expect(turns[1]?.prompt?.text).toBe('next');
  });

  it('attaches system and meta lines to the nearest turn instead of starting one', () => {
    const turns = buildTurns([
      user(0, 'go'),
      { lineNo: 1, role: 'system', blocks: [{ type: 'text', text: 'compacted' }] },
      user(2, '<system-reminder>be careful</system-reminder>'),
      say(3, 'ok'),
    ]);
    expect(turns).toHaveLength(1);
    expect(turns[0]?.rows.map((r) => r.kind)).toEqual(['note', 'note', 'text']);
  });

  it('pairs a result with its call by id and marks failures', () => {
    const [turn] = buildTurns([
      user(0, 'go'),
      call(1, 'a', 'Bash', { command: 'false' }),
      call(2, 'b', 'Bash', { command: 'true' }),
      result(3, 'b', 'fine'),
      result(4, 'a', 'exit 1', true),
    ]);
    // a failed result keeps its own row, and two tools are not enough to fold
    expect(turn?.rows.map((r) => r.kind)).toEqual(['tool', 'tool']);
    const tools = (turn?.rows ?? []) as ToolRow[];
    expect(tools.map((t) => [t.arg, t.result?.text, t.result?.failed])).toEqual([
      ['false', 'exit 1', true],
      ['true', 'fine', false],
    ]);
  });

  it('folds consecutive tools into one row, but not a lone tool, an edit or a tool with an elided result', () => {
    const [turn] = buildTurns([
      user(0, 'go'),
      call(1, 'a', 'Read', { file_path: '/p/a.ts' }),
      {
        lineNo: 2,
        role: 'user',
        blocks: [{ elided: true, type: 'tool-result', bytes: 99_999, preview: 'xx', ref: '2/0' }],
      },
      call(3, 'b', 'Edit', { file_path: '/p/a.ts', old_string: 'a', new_string: 'b' }),
      call(4, 'c', 'Bash', { command: 'x' }),
      call(5, 'd', 'Grep', { pattern: 'y' }),
      call(6, 'e', 'WebFetch', { url: 'https://x' }),
      say(7, 'done'),
    ]);
    expect(turn?.rows.map((r) => r.kind)).toEqual(['tool', 'edit', 'fold', 'text']);
    const read = turn?.rows[0] as ToolRow;
    expect(read.result?.elided).toMatchObject({ bytes: 99_999, line: 2, block: 0 });
    expect(turn?.rows[2]).toMatchObject({
      kind: 'fold',
      tools: [{ tool: 'Bash' }, { tool: 'Grep' }, { tool: 'WebFetch' }],
    });
  });

  it('folds three or more rows, but a failed one stays out of the run', () => {
    const [turn] = buildTurns([
      user(0, 'go'),
      call(1, 'a', 'Bash', { command: 'a' }),
      result(2, 'a', 'ok'),
      call(3, 'b', 'Bash', { command: 'b' }),
      result(4, 'b', 'boom', true),
      call(5, 'c', 'Grep', { pattern: 'c' }),
      call(6, 'd', 'Bash', { command: 'd' }),
      call(7, 'e', 'mcp__github__list_prs', { owner: 'x' }),
      call(8, 'f', 'LS', { path: '/p' }),
    ]);
    expect(turn?.rows.map((r) => r.kind)).toEqual(['tool', 'tool', 'fold']);
    expect(turn?.rows[2]).toMatchObject({
      tools: [
        { tool: 'Grep' },
        { tool: 'Bash' },
        { tool: 'mcp__github__list_prs' },
        { tool: 'LS' },
      ],
    });
  });

  it('keeps Write as a call of its own, with the time of the call and of its result', () => {
    const [turn] = buildTurns([
      user(0, 'go'),
      {
        lineNo: 1,
        role: 'assistant',
        ts: '2026-10-05T09:00:00.000',
        blocks: [
          {
            type: 'tool-call',
            id: 'w',
            name: 'Write',
            kind: 'write',
            input: { file_path: '/p/a.ts', content: 'x\ny\n' },
          },
        ],
      },
      {
        lineNo: 2,
        role: 'user',
        ts: '2026-10-05T09:00:00.400',
        blocks: [{ type: 'tool-result', id: 'w', content: 'created' }],
      },
    ]);
    const write = turn?.rows[0] as ToolRow;
    expect(write.kind).toBe('tool');
    expect(write.ts).toBe('2026-10-05T09:00:00.000');
    expect(write.result?.ts).toBe('2026-10-05T09:00:00.400');
  });

  it('shows a file edit as a diff with the added and removed counts', () => {
    const [turn] = buildTurns(
      [
        user(0, 'go'),
        call(1, 'a', 'Edit', {
          file_path: '/repo/web/items.ts',
          old_string: 'one\ntwo\nthree',
          new_string: 'one\nTWO\nextra\nthree',
        }),
      ],
      { cwd: '/repo' },
    );
    const edit = turn?.rows[0] as EditRow;
    expect(edit.path).toBe('web/items.ts');
    expect([edit.added, edit.removed]).toEqual([2, 1]);
    expect(edit.rows.map((r) => r.kind)).toEqual(['eq', 'del', 'add', 'add', 'eq']);
  });

  it('summarizes files, tools and duration', () => {
    const [turn] = buildTurns([
      user(0, 'go', '00:00'),
      call(1, 'a', 'Read', { file_path: '/p/c.ts' }, '00:10'),
      call(2, 'b', 'Edit', { file_path: '/p/a.ts', old_string: 'a', new_string: 'b' }, '00:20'),
      call(3, 'c', 'Edit', { file_path: '/p/b.ts', old_string: 'a', new_string: 'b' }, '00:30'),
      say(4, 'done', '01:08'),
    ]);
    // a file only read is not a file changed
    expect(turn?.summary).toEqual({ files: 2, tools: 3, durationMs: 68_000 });
  });

  it('keeps an elided prompt as its preview', () => {
    const [turn] = buildTurns([
      {
        lineNo: 0,
        role: 'user',
        blocks: [
          { elided: true, type: 'text', bytes: 20_000, preview: 'a very long prompt…', ref: '0/0' },
        ],
      },
    ]);
    expect(turn?.prompt).toMatchObject({ text: 'a very long prompt…', elided: { bytes: 20_000 } });
  });
});

describe('command turns', () => {
  it('reads the command from the tags and from typed text', () => {
    expect(
      parseCommand(
        '<command-name>/review</command-name><command-message>review</command-message><command-args>147</command-args>',
      ),
    ).toEqual({ name: 'review', args: '147' });
    expect(parseCommand('/hello world')).toEqual({ name: 'hello', args: 'world' });
    expect(parseCommand('/context')).toEqual({ name: 'context', args: '' });
    expect(parseCommand('see /tmp')).toBeNull();
  });

  it('strips terminal escapes and formats token counts', () => {
    expect(stripAnsi('\u001b[2mCompacted \u001b[22m')).toBe('Compacted ');
    expect(formatTokens(26208)).toBe('26.2k');
    expect(formatTokens(840)).toBe('840');
  });

  it('turns a command prompt into a chip and its output into a command row', () => {
    const turns = buildTurns([
      user(1, '/context'),
      user(2, '<local-command-stdout>62.4k of 200k</local-command-stdout>'),
    ]);
    expect(turns[0]?.prompt?.command).toEqual({ name: 'context', args: '' });
    expect(turns[0]?.rows[0]).toMatchObject({
      kind: 'command',
      command: 'context',
      text: '62.4k of 200k',
    });
  });

  const boundary = (lineNo: number, trigger: string): Entry => ({
    lineNo,
    role: 'system',
    blocks: [
      {
        type: 'other',
        kind: 'compact_boundary',
        label: 'Conversation compacted',
        text: JSON.stringify({ trigger, preTokens: 45100, postTokens: 3200 }),
      },
    ],
  });

  it('moves the boundary written before /compact next to its output, with the sizes on the block', () => {
    const turns = buildTurns([
      user(1, 'a prompt'),
      say(2, 'an answer'),
      boundary(3, 'manual'),
      user(4, '/compact'),
      user(5, '<local-command-stdout>\u001b[2mCompacted \u001b[22m</local-command-stdout>'),
    ]);
    expect(turns.map((t) => t.rows.map((r) => r.kind))).toEqual([
      ['text'],
      ['command', 'boundary'],
    ]);
    expect(turns[1]?.rows[0]).toMatchObject({
      kind: 'command',
      text: 'Compacted',
      compact: { from: '45.1k', to: '3.2k' },
    });
    expect(turns[1]?.rows[1]).not.toHaveProperty('sizes');
  });

  it('keeps an automatic compaction as one divider with the sizes', () => {
    const turns = buildTurns([
      user(1, 'a prompt'),
      say(2, 'an answer'),
      boundary(3, 'auto'),
      user(4, 'next'),
    ]);
    expect(turns[0]?.rows[1]).toMatchObject({
      kind: 'boundary',
      sizes: { from: '45.1k', to: '3.2k' },
    });
    expect(turns).toHaveLength(2);
  });
});

/** The entries the core projects from a real Claude Code 2.1.300 transcript (core/test/projection.test.ts pins them). */
const realEntries = JSON.parse(
  readFileSync(
    path.resolve(import.meta.dirname, '../../shared/test/fixtures/claude-commands.entries.json'),
    'utf8',
  ),
) as Entry[];

describe('a real transcript with /context and /compact', () => {
  const turns = buildTurns(realEntries);

  it('has one turn per prompt and no turn for the hidden summary or caveats', () => {
    expect(turns.map((t) => t.prompt?.text)).toEqual([
      '/context',
      'say hi in one word',
      '/compact',
    ]);
  });

  it('draws /context as a chip and a block holding the markdown table', () => {
    expect(turns[0]?.prompt?.command).toEqual({ name: 'context', args: '' });
    expect(turns[0]?.rows).toHaveLength(1);
    const block = turns[0]?.rows[0];
    expect(block).toMatchObject({ kind: 'command', command: 'context' });
    const out = block?.kind === 'command' ? block.text : '';
    expect(out).toMatch(/^## Context Usage/);
    expect(out).toContain('| Category | Tokens | Percentage |');
    expect(out).not.toContain('local-command-stdout');
  });

  it('draws /compact as a chip, a Compacted block with the sizes and one divider after it', () => {
    expect(turns[2]?.prompt?.command).toEqual({ name: 'compact', args: '' });
    const rows = turns[2]?.rows ?? [];
    expect(rows.map((r) => r.kind)).toEqual(['command', 'boundary']);
    expect(rows[0]).toMatchObject({
      command: 'compact',
      text: 'Compacted',
      compact: { from: '26.2k', to: '1.2k' },
    });
    expect(turns.flatMap((t) => t.rows).filter((r) => r.kind === 'boundary')).toHaveLength(1);
    expect(turns[1]?.rows.map((r) => r.kind)).toEqual(['text']);
  });

  it('shows the same boundary of an automatic compaction as a divider with the sizes', () => {
    const auto = realEntries.filter((e) => e.lineNo <= 5);
    const rows = buildTurns(auto).flatMap((t) => t.rows);
    expect(rows.at(-1)).toMatchObject({
      kind: 'boundary',
      text: 'Conversation compacted',
      sizes: { from: '26.2k', to: '1.2k' },
    });
  });
});

describe('steered messages', () => {
  const steered = (lineNo: number, text: string): Entry => ({
    ...user(lineNo, text, '00:07'),
    steered: true,
  });

  it('lands a steered message in the running turn as a steer row', () => {
    const turns = buildTurns([user(1, 'go'), say(2, 'working'), steered(3, 'also this')]);
    expect(turns).toHaveLength(1);
    const rows = turns[0]?.rows ?? [];
    expect(rows.find((r) => r.kind === 'steer')).toMatchObject({
      kind: 'steer',
      text: 'also this',
    });
  });

  it('starts a turn when no turn is open', () => {
    const turns = buildTurns([steered(1, 'first')]);
    expect(turns).toHaveLength(1);
    expect(turns[0]?.rows.some((r) => r.kind === 'steer')).toBe(false);
  });
});
