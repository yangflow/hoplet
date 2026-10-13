import type { ToolKind } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  formatSpan,
  metaParts,
  numberedLines,
  type ToolRef,
  toolObject,
  toolSpec,
  verbsOf,
} from '../src/screens/cockpit/conversation/tools.ts';

const tool = (name: string, kind: ToolKind): ToolRef => ({ name, kind });

describe('the verb table', () => {
  it('gives each kind of tool the past-tense verb and icon of the design', () => {
    const kinds: ToolKind[] = [
      'read',
      'edit',
      'write',
      'run',
      'search',
      'list',
      'fetch',
      'call',
      'view',
    ];
    expect(kinds.map((kind) => toolSpec(tool('x', kind)).verb)).toEqual([
      'Read',
      'Edited',
      'Wrote',
      'Ran',
      'Searched',
      'Listed',
      'Fetched',
      'Called',
      'Viewed',
    ]);
    expect(toolSpec(tool('Bash', 'run')).icon).toBe('terminal');
  });

  it('calls MCP tools by server and tool', () => {
    const spec = toolSpec({
      name: 'mcp__github__list_prs',
      kind: 'call',
      mcp: { server: 'github', tool: 'list_prs' },
    });
    expect(spec).toMatchObject({
      verb: 'Called',
      kind: 'call',
      mcp: { server: 'github', tool: 'list_prs' },
    });
    expect(toolObject(spec, '')).toBe('github › list_prs');
  });

  it('gives a tool of kind other its own name as the verb', () => {
    expect(toolSpec(tool('TodoWrite', 'other'))).toMatchObject({
      verb: 'TodoWrite',
      kind: 'other',
    });
    expect(toolSpec(tool(' ', 'other'))).toMatchObject({ verb: 'Tool', kind: 'other' });
  });

  it('names the distinct verbs of some tools once, in order', () => {
    expect(
      verbsOf([
        tool('Bash', 'run'),
        tool('mcp__a__b', 'call'),
        tool('BashOutput', 'run'),
        tool('WebFetch', 'fetch'),
        tool('image', 'view'),
      ]),
    ).toEqual(['Ran', 'Called', 'Fetched', 'Viewed']);
  });

  it('writes searches as quoted queries and fetches as hosts', () => {
    expect(toolObject(toolSpec(tool('Grep', 'search')), 'appendChunk')).toBe('"appendChunk"');
    expect(toolObject(toolSpec(tool('WebFetch', 'fetch')), 'https://example.com/a/b')).toBe(
      'example.com',
    );
    expect(toolObject(toolSpec(tool('Read', 'read')), 'core/a.ts')).toBe('core/a.ts');
  });
});

const ok = (text: string, bytes = text.length) => ({ text, failed: false, bytes });

describe('meta', () => {
  it('gives a read its line range and size', () => {
    const text = '     1→a\n     2→b\n     3→c';
    expect(
      metaParts({ name: 'Read', kind: 'read', input: { file_path: 'a' }, result: ok(text, 6250) }),
    ).toEqual(['L1–3', '6.1 KiB']);
    expect(
      metaParts({
        name: 'Read',
        kind: 'read',
        input: { file_path: 'a', offset: 30, limit: 7 },
        result: { ...ok('', 412 * 1024), elided: true },
      }),
    ).toEqual(['L30–36', '412 KiB']);
  });

  it('gives a command its exit code and duration', () => {
    const ran = (result: ReturnType<typeof ok>, span: number) =>
      metaParts({
        name: 'Bash',
        kind: 'run',
        input: { command: 'x' },
        result,
        startedAt: 1000,
        endedAt: 1000 + span,
      });
    expect(ran(ok('fine'), 100)).toEqual(['exit 0', '0.1 s']);
    expect(ran({ text: 'Exit code 1\nboom', failed: true, bytes: 16 }, 42_000)).toEqual([
      'exit 1',
      '42 s',
    ]);
    expect(ran({ text: 'boom', failed: true, bytes: 4 }, 1500)).toEqual(['Failed', '1.5 s']);
  });

  it('omits what the data does not carry', () => {
    expect(
      metaParts({ name: 'Bash', kind: 'run', input: { command: 'x' }, result: ok('fine') }),
    ).toEqual(['exit 0']);
    expect(
      metaParts({
        name: 'WebFetch',
        kind: 'fetch',
        input: { url: 'https://x' },
        result: ok('page', 18 * 1024),
      }),
    ).toEqual(['18 KiB']);
    expect(metaParts({ name: 'Bash', kind: 'run', input: {} })).toEqual([]);
    expect(metaParts({ name: 'TodoWrite', kind: 'other', input: {} })).toEqual([]);
  });

  it('counts matches, files and entries', () => {
    const grep = (input: object, text: string) =>
      metaParts({ name: 'Grep', kind: 'search', input, result: ok(text) });
    expect(grep({ output_mode: 'content' }, 'a.ts:1:x\nb.ts:2:y\nc.ts:3:z')).toEqual(['3 matches']);
    expect(grep({}, 'Found 2 files\na.ts\nb.ts')).toEqual(['2 files']);
    expect(grep({ output_mode: 'content' }, 'No matches found')).toEqual(['0 matches']);
    expect(
      metaParts({ name: 'LS', kind: 'list', input: {}, result: ok('- /p/\n  - a.ts\n  - src/') }),
    ).toEqual(['2 entries']);
  });

  it('gives a write its lines and size, and an MCP call its items and duration', () => {
    expect(metaParts({ name: 'Write', kind: 'write', input: { content: 'a\nb\n' } })).toEqual([
      '2 lines',
      '4 B',
    ]);
    expect(
      metaParts({
        name: 'mcp__github__list_prs',
        kind: 'call',
        mcp: { server: 'github', tool: 'list_prs' },
        input: {},
        result: ok('[1,2,3]'),
        startedAt: 0,
        endedAt: 600,
      }),
    ).toEqual(['3 items', '0.6 s']);
  });

  it('marks a failed call that is not a command', () => {
    expect(
      metaParts({
        name: 'WebFetch',
        kind: 'fetch',
        input: {},
        result: { text: 'Request failed with status code 404', failed: true, bytes: 35 },
      }),
    ).toEqual(['Failed', '404', '35 B']);
  });
});

describe('spans and numbered lines', () => {
  it('formats a span at the precision it deserves', () => {
    expect([formatSpan(100), formatSpan(9_940), formatSpan(42_000), formatSpan(68_000)]).toEqual([
      '0.1 s',
      '9.9 s',
      '42 s',
      '1m 08s',
    ]);
  });

  it('reads cat -n output and nothing else', () => {
    expect(numberedLines('     1→a\n     2→b\n')).toEqual([
      { no: 1, text: 'a' },
      { no: 2, text: 'b' },
    ]);
    expect(numberedLines('a\nb')).toBeNull();
  });
});
