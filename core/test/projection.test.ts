import { readFileSync } from 'node:fs';
import path from 'node:path';
import {
  type Block,
  type Entry,
  EntrySchema,
  type FullBlock,
  FullBlockSchema,
  is,
} from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  blockOfLine,
  classifyTool,
  ELIDE_ABOVE_BYTES,
  excerptOf,
  fullEntry,
  PREVIEW_BYTES,
  projectLine,
} from '../src/api/projection/index.ts';
import { claudeCommands } from './fixtures/claude-commands.ts';
import { assistant, claudeGrammar, userResult, userText } from './fixtures/claude-grammar.ts';

function trace(entry: Entry | null): string {
  if (!entry) return '-';
  return `${entry.role}:${entry.blocks.map((b) => ('elided' in b ? `elided-${b.type}` : b.type)).join('+')}`;
}

describe('Claude mapping', () => {
  it('maps every grammar fixture line onto the conversation model', () => {
    const traces = claudeGrammar.map(({ line }, i) => trace(projectLine('claude', line, i)));
    expect(traces).toEqual(claudeGrammar.map((f) => f.trace));
  });

  it('produces entries the shared schema accepts', () => {
    claudeGrammar.forEach(({ line }, i) => {
      const entry = projectLine('claude', line, i);
      if (entry) expect(is(EntrySchema, entry)).toBe(true);
    });
  });

  it('keeps the kind of the compact boundary', () => {
    const line = { type: 'system', subtype: 'compact_boundary', content: 'Conversation compacted' };
    expect(projectLine('claude', line, 3)?.blocks).toEqual([
      { type: 'other', kind: 'compact_boundary', text: 'Conversation compacted' },
    ]);
  });

  it('projects the real /context and /compact transcript onto the entries the web tests build turns from', () => {
    const entries = claudeCommands.flatMap((line, i) => projectLine('claude', line, i) ?? []);
    const expected = JSON.parse(
      readFileSync(
        path.resolve(
          import.meta.dirname,
          '../../shared/test/fixtures/claude-commands.entries.json',
        ),
        'utf8',
      ),
    );
    expect(entries).toEqual(expected);
    expect(JSON.stringify(entries)).not.toContain('local-command-caveat');
    expect(JSON.stringify(entries)).not.toContain('This session is being continued');
  });

  it('carries the sizes of a compaction in the boundary and hides its summary', () => {
    const [caveat, command, stdout, , , boundary, summary, , compact, printed] = claudeCommands;
    expect(projectLine('claude', boundary, 5)?.blocks).toEqual([
      {
        type: 'other',
        kind: 'compact_boundary',
        label: 'Conversation compacted',
        text: '{"trigger":"manual","preTokens":26208,"postTokens":1151}',
      },
    ]);
    expect(projectLine('claude', summary, 6)).toBeNull();
    expect(projectLine('claude', caveat, 0)).toBeNull();
    expect(projectLine('claude', command, 1)?.blocks[0]).toMatchObject({ type: 'text' });
    expect(projectLine('claude', stdout, 2)?.role).toBe('system');
    expect(projectLine('claude', compact, 8)?.role).toBe('user');
    expect(projectLine('claude', printed, 9)?.blocks).toEqual([
      {
        type: 'text',
        text: '<local-command-stdout>\u001b[2mCompacted \u001b[22m</local-command-stdout>',
      },
    ]);
  });

  it('keeps line numbers and timestamps', () => {
    const entry = projectLine('claude', userText('hi'), 7);
    expect(entry).toMatchObject({ lineNo: 7, role: 'user', ts: '2026-10-05T09:00:00Z' });
  });

  it('maps tool calls, results, errors and images', () => {
    const call = fullEntry(
      'claude',
      assistant([{ type: 'tool_use', id: 't1', name: 'Bash', input: { command: 'ls' } }]),
      0,
    );
    expect(call?.blocks).toEqual([
      { type: 'tool-call', id: 't1', name: 'Bash', kind: 'run', input: { command: 'ls' } },
    ]);
    const result = fullEntry(
      'claude',
      userResult('t1', [{ type: 'text', text: 'boom' }, { type: 'image' }], undefined, true),
      1,
    );
    expect(result?.blocks).toEqual([
      { type: 'tool-result', id: 't1', content: 'boom\n[image]', isError: true },
    ]);
    const image = fullEntry(
      'claude',
      assistant([
        { type: 'image', source: { type: 'base64', media_type: 'image/png', data: 'AAAA' } },
      ]),
      2,
    );
    expect(image?.blocks).toEqual([{ type: 'image', mediaType: 'image/png', data: 'AAAA' }]);
  });

  it('turns lines it cannot map into other blocks, never an error', () => {
    expect(fullEntry('claude', { raw: '{"x', _parseError: true }, 3)?.blocks).toEqual([
      { type: 'other', kind: 'unparsed', text: '{"x' },
    ]);
    const unknown = fullEntry('claude', { type: 'progress', elapsed: 1 }, 4);
    expect(unknown?.blocks[0]).toMatchObject({ type: 'other', kind: 'progress' });
    const attachment = fullEntry(
      'claude',
      { type: 'attachment', attachment: { type: 'file', file_path: '/tmp/shot.png' } },
      5,
    );
    expect(attachment?.blocks[0]).toMatchObject({
      type: 'other',
      kind: 'attachment',
      label: 'shot.png',
    });
    const odd = fullEntry('claude', assistant([{ type: 'server_tool_use', id: 'x' }]), 6);
    expect(odd?.blocks[0]).toMatchObject({ type: 'other', kind: 'server_tool_use' });
  });
});

describe('Claude tool kinds', () => {
  const kinds = (names: string[]) => names.map((n) => classifyTool('claude', n).kind);

  it('maps Claude Code tools to the kinds of the conversation model', () => {
    expect(kinds(['Read', 'NotebookRead'])).toEqual(['read', 'read']);
    expect(kinds(['Edit', 'MultiEdit', 'NotebookEdit'])).toEqual(['edit', 'edit', 'edit']);
    expect(kinds(['Write'])).toEqual(['write']);
    expect(kinds(['Bash', 'BashOutput', 'KillShell'])).toEqual(['run', 'run', 'run']);
    expect(kinds(['Grep', 'Glob', 'WebSearch'])).toEqual(['search', 'search', 'search']);
    expect(kinds(['LS', 'WebFetch'])).toEqual(['list', 'fetch']);
    expect(kinds(['TodoWrite', 'Task', 'bash'])).toEqual(['other', 'other', 'other']);
  });

  it('calls MCP tools by server and tool', () => {
    expect(classifyTool('claude', 'mcp__github__list_prs')).toEqual({
      kind: 'call',
      mcp: { server: 'github', tool: 'list_prs' },
    });
    const call = fullEntry(
      'claude',
      assistant([{ type: 'tool_use', id: 't', name: 'mcp__a__b_c', input: {} }]),
      0,
    );
    expect(call?.blocks[0]).toMatchObject({ kind: 'call', mcp: { server: 'a', tool: 'b_c' } });
  });
});

describe('Claude Code internal records', () => {
  const attachment = (att: Record<string, unknown>) => ({ type: 'attachment', attachment: att });

  it('drops deferred-tools records and any attachment the user did not attach', () => {
    for (const line of [
      { type: 'deferred_tools_record', names: ['a'] },
      { type: 'deferred_tools_delta' },
      attachment({ type: 'deferred_tools_delta', addedNames: ['x'] }),
      attachment({ type: 'mcp_instructions_record', text: 'y' }),
      attachment({ type: 'todo_reminder', content: [] }),
      attachment({ text: 'no type and nothing named' }),
    ]) {
      expect(projectLine('claude', line, 0)).toBeNull();
    }
  });

  it('shows a message the user steered into a turn as a steered user entry, and drops the rest of the queue records', () => {
    const queued = (over: Record<string, unknown>) =>
      attachment({
        type: 'queued_command',
        prompt: 'also run the Go tests',
        commandMode: 'prompt',
        ...over,
      });
    const steered = projectLine('claude', queued({}), 7);
    expect(steered).toMatchObject({
      lineNo: 7,
      role: 'user',
      steered: true,
      blocks: [{ type: 'text', text: 'also run the Go tests' }],
    });
    expect(projectLine('claude', queued({ isMeta: true }), 8)).toBeNull();
    expect(projectLine('claude', queued({ origin: { kind: 'task-notification' } }), 9)).toBeNull();
    expect(projectLine('claude', { type: 'queue-operation', operation: 'enqueue' }, 10)).toBeNull();
  });

  it('keeps a file or image the user attached', () => {
    const labels = [
      attachment({ type: 'file', file_path: '/w/a.ts' }),
      attachment({ type: 'image', media_type: 'image/png' }),
      attachment({ name: 'notes.md' }),
    ].map((line) => {
      const block = projectLine('claude', line, 0)?.blocks[0];
      return block && !('elided' in block) && block.type === 'other' ? block.label : null;
    });
    expect(labels).toEqual(['a.ts', 'image/png', 'notes.md']);
  });

  it('drops a large record before elision and keeps block indexes of a line consistent', () => {
    const big = 'r'.repeat(20_000);
    const record = { type: 'deferred_tools_delta', added: big };
    expect(projectLine('claude', record, 1)).toBeNull();

    const line = assistant([
      { type: 'deferred_tools_record', names: [big] },
      { type: 'text', text: big },
    ]);
    const entry = projectLine('claude', line, 2);
    expect(entry?.blocks).toEqual([
      expect.objectContaining({ elided: true, type: 'text', ref: '2/0' }),
    ]);
    expect(blockOfLine('claude', line, 2, 0)).toEqual({ type: 'text', text: big });
    expect(blockOfLine('claude', line, 2, 1)).toBeNull();
  });
});

/** A text block whose encoded size is exactly `bytes`. */
function textBlockOfSize(bytes: number): string {
  const overhead = Buffer.byteLength(JSON.stringify({ type: 'text', text: '' }));
  return 'x'.repeat(bytes - overhead);
}

const sizeOf = (block: FullBlock) => Buffer.byteLength(JSON.stringify(block));

describe('elision', () => {
  it('keeps a block of exactly 8 KiB and elides one byte more', () => {
    const at = projectLine('claude', userText(textBlockOfSize(ELIDE_ABOVE_BYTES)), 0);
    expect(at?.blocks[0]).toMatchObject({ type: 'text' });
    expect('elided' in (at?.blocks[0] ?? {})).toBe(false);

    const over = projectLine('claude', userText(textBlockOfSize(ELIDE_ABOVE_BYTES + 1)), 0);
    expect(over?.blocks[0]).toEqual({
      elided: true,
      type: 'text',
      bytes: ELIDE_ABOVE_BYTES + 1,
      preview: 'x'.repeat(PREVIEW_BYTES),
      ref: '0/0',
    });
  });

  it('measures the encoded size, so multi-byte text counts its bytes', () => {
    const text = '字'.repeat(3000);
    const full = fullEntry('claude', userText(text), 0)?.blocks[0] as FullBlock;
    expect(sizeOf(full)).toBeGreaterThan(ELIDE_ABOVE_BYTES);
    const block = projectLine('claude', userText(text), 0)?.blocks[0];
    expect(block).toMatchObject({ elided: true, type: 'text' });
  });

  it('cuts the preview at 1 KiB of UTF-8 without splitting a character', () => {
    const block = projectLine('claude', userText('字'.repeat(5000)), 0)?.blocks[0] as Block;
    if (!('elided' in block)) throw new Error('expected an elided block');
    expect(Buffer.byteLength(block.preview)).toBeLessThanOrEqual(PREVIEW_BYTES);
    expect(block.preview).toBe('字'.repeat(341));
    const emoji = projectLine('claude', userText('😀'.repeat(5000)), 0)?.blocks[0] as Block;
    if (!('elided' in emoji)) throw new Error('expected an elided block');
    expect(Buffer.byteLength(emoji.preview)).toBeLessThanOrEqual(PREVIEW_BYTES);
    expect(emoji.preview).toBe('😀'.repeat(256));
  });

  it('elides tool results and tool calls with a text preview, and images without one', () => {
    const big = 'r'.repeat(20_000);
    const entry = projectLine(
      'claude',
      {
        type: 'assistant',
        message: {
          content: [
            { type: 'tool_use', id: 't', name: 'Write', input: { content: big } },
            { type: 'image', source: { type: 'base64', media_type: 'image/png', data: big } },
            { type: 'text', text: 'short' },
          ],
        },
      },
      9,
    );
    const [call, image, text] = entry?.blocks ?? [];
    expect(call).toMatchObject({ elided: true, type: 'tool-call', ref: '9/0' });
    expect((call as { preview: string }).preview.startsWith('{"content":"rrr')).toBe(true);
    expect(image).toMatchObject({ elided: true, type: 'image', preview: '', ref: '9/1' });
    expect(text).toEqual({ type: 'text', text: 'short' });
    const result = projectLine('claude', userResult('t', big), 10)?.blocks[0];
    expect(result).toMatchObject({ elided: true, type: 'tool-result', ref: '10/0' });
  });

  it('returns the full block for a reference, equal to the original content', () => {
    const big = `${'line of tool output\n'.repeat(2000)}end`;
    const line = userResult('t', big);
    const block = blockOfLine('claude', line, 4, 0);
    expect(block).toEqual({ type: 'tool-result', id: 't', content: big });
    expect(is(FullBlockSchema, block)).toBe(true);
    expect(blockOfLine('claude', line, 4, 1)).toBeNull();
    expect(blockOfLine('claude', { type: 'mode' }, 4, 0)).toBeNull();
  });
});

describe('excerpts', () => {
  it('centres the excerpt on the match and collapses whitespace', () => {
    const text = `${'a '.repeat(200)}the Needle\n\nis here ${'b '.repeat(200)}`;
    const excerpt = excerptOf('claude', userText(text), 0, 'needle');
    expect(excerpt).toContain('the Needle is here');
    expect(excerpt.length).toBeLessThanOrEqual(160);
  });

  it('starts at the beginning when the match lies outside the entry text, and is empty without an entry', () => {
    expect(excerptOf('claude', { ...userText('hello world'), cwd: '/needle' }, 0, 'needle')).toBe(
      'hello world',
    );
    expect(excerptOf('claude', { type: 'mode', mode: 'needle' }, 0, 'needle')).toBe('');
  });

  it('never returns more than a short excerpt of a huge block', () => {
    const excerpt = excerptOf(
      'claude',
      userResult('t', `${'z'.repeat(1_000_000)}needle`),
      0,
      'needle',
    );
    expect(excerpt.length).toBeLessThanOrEqual(160);
  });
});
