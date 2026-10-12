/**
 * Claude Code JSONL lines covering the conversation grammar: messages, thinking, tools of every class,
 * images, attachments, system lines and the bookkeeping lines the projection drops. `claudeTrace` is the
 * expected mapping, one string per line: `role:type+type` over the entry blocks, or `-` when the line has no entry.
 */
const TS = '2026-10-05T09:00:00Z';
const TS2 = '2026-10-05T09:00:05Z';
const CWD = '/Users/mark/github/yangflow/repopro/hoplet';

type Line = Record<string, unknown>;

export function userText(text: string, ts = TS): Line {
  return { type: 'user', timestamp: ts, cwd: CWD, message: { role: 'user', content: text } };
}

export function userResult(id: string, content: unknown, ts = TS2, isError?: boolean): Line {
  return {
    type: 'user',
    timestamp: ts,
    cwd: CWD,
    message: {
      role: 'user',
      content: [
        { type: 'tool_result', tool_use_id: id, content, ...(isError ? { is_error: true } : {}) },
      ],
    },
  };
}

export function assistant(blocks: Line[], ts = TS2): Line {
  return {
    type: 'assistant',
    timestamp: ts,
    cwd: CWD,
    message: { role: 'assistant', content: blocks },
  };
}

export const claudeGrammar: { line: unknown; trace: string }[] = [
  {
    line: userText('The 80 MB session is slow to open. Can we stream only new bytes?'),
    trace: 'user:text',
  },
  {
    line: assistant([{ type: 'thinking', thinking: 'Byte cursor vs whole-file read.' }]),
    trace: 'assistant:thinking',
  },
  {
    line: assistant([
      { type: 'text', text: 'Yes. The file only appends, so the old EOF offset is the cursor.' },
    ]),
    trace: 'assistant:text',
  },
  {
    line: assistant([
      { type: 'tool_use', id: 'c-read', name: 'Read', input: { file_path: 'c-3.ndjson' } },
    ]),
    trace: 'assistant:tool-call',
  },
  { line: userResult('c-read', '{"line":1}'), trace: 'user:tool-result' },
  {
    line: assistant([
      {
        type: 'tool_use',
        id: 'c-edit',
        name: 'Edit',
        input: { file_path: 'lines.ts', old_string: 'a', new_string: 'b' },
      },
    ]),
    trace: 'assistant:tool-call',
  },
  { line: userResult('c-edit', 'ok'), trace: 'user:tool-result' },
  {
    line: assistant([
      { type: 'tool_use', id: 'c-bash', name: 'Bash', input: { command: 'ls core' } },
    ]),
    trace: 'assistant:tool-call',
  },
  { line: userResult('c-bash', 'src\n'), trace: 'user:tool-result' },
  {
    line: assistant([
      { type: 'tool_use', id: 'c-mcp', name: 'mcp__github__list_prs', input: { owner: 'o' } },
    ]),
    trace: 'assistant:tool-call',
  },
  { line: userResult('c-mcp', '[]'), trace: 'user:tool-result' },
  {
    line: assistant([
      { type: 'tool_use', id: 'c-skill', name: 'Skill', input: { path: 'handbook/agents.md' } },
    ]),
    trace: 'assistant:tool-call',
  },
  {
    line: assistant([
      { type: 'tool_use', id: 'c-web', name: 'WebFetch', input: { url: 'https://example.com' } },
    ]),
    trace: 'assistant:tool-call',
  },
  {
    line: assistant([
      { type: 'thinking', thinking: 'plan' },
      { type: 'text', text: 'Verified on a 23 MB fixture.' },
      { type: 'tool_use', id: 'c-two', name: 'Read', input: {} },
    ]),
    trace: 'assistant:thinking+text+tool-call',
  },
  {
    line: assistant([
      { type: 'image', source: { type: 'base64', media_type: 'image/png', data: 'AAAA' } },
    ]),
    trace: 'assistant:image',
  },
  {
    line: {
      type: 'user',
      timestamp: TS,
      message: {
        role: 'user',
        content: [
          { type: 'text', text: 'look at this' },
          { type: 'image', source: { type: 'base64', media_type: 'image/jpeg', data: 'BBBB' } },
        ],
      },
    },
    trace: 'user:text+image',
  },
  {
    line: userResult('c-fail', [{ type: 'text', text: 'boom' }, { type: 'image' }], TS2, true),
    trace: 'user:tool-result',
  },
  {
    line: {
      type: 'attachment',
      timestamp: TS2,
      cwd: CWD,
      attachment: { type: 'file', file_path: '/tmp/shot.png' },
    },
    trace: 'system:other',
  },
  { line: { type: 'system', timestamp: TS2, content: 'session started' }, trace: 'system:text' },
  { line: { type: 'progress', timestamp: TS2, elapsed: 1 }, trace: 'system:other' },
  { line: { raw: '{"broken', _parseError: true }, trace: 'system:other' },
  { line: 42, trace: 'system:other' },
  { line: { type: 'last-prompt', timestamp: TS2 }, trace: '-' },
  { line: { type: 'ai-title', timestamp: TS2, aiTitle: 'byte cursor' }, trace: '-' },
  { line: { type: 'permission-mode', timestamp: TS2, permissionMode: 'auto' }, trace: '-' },
  { line: { type: 'atis-latch', timestamp: TS2, atis: '' }, trace: '-' },
  { line: { type: 'cost-state', timestamp: TS2, totalCostUSD: 0 }, trace: '-' },
  { line: { type: 'mode', timestamp: TS2, mode: 'normal' }, trace: '-' },
  { line: { type: 'queue-operation', timestamp: TS2, operation: 'enqueue' }, trace: '-' },
  { line: { type: 'file-history-snapshot', snapshot: {} }, trace: '-' },
  {
    line: {
      type: 'attachment',
      timestamp: TS2,
      attachment: { type: 'mcp_instructions_delta', added: [] },
    },
    trace: '-',
  },
  {
    line: { type: 'system', timestamp: TS2, subtype: 'turn_duration', durationMs: 1000 },
    trace: '-',
  },
  {
    line: { type: 'system', timestamp: TS2, subtype: 'away_summary', content: 'left' },
    trace: '-',
  },
  { line: { type: 'user', isSidechain: true, message: { content: 'side' } }, trace: '-' },
  { line: { type: 'user', isMeta: true, message: { content: 'meta' } }, trace: '-' },
  { line: { type: 'user', message: { content: '   ' } }, trace: '-' },
];
