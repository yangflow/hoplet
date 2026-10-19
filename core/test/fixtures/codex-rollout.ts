/**
 * Synthetic Codex rollout lines in the shapes Codex writes: `session_meta` with ~70 KB of base instructions,
 * the `response_item` records (model I/O) and the `event_msg` `item_completed` records the mapping reads.
 */
type Rec = Record<string, unknown>;

let ordinal = 0;
export const record = (type: string, payload: Rec): Rec => ({
  timestamp: '2026-10-05T09:00:00.000Z',
  ordinal: ordinal++,
  type,
  payload,
});

export const item = (body: Rec): Rec =>
  record('event_msg', {
    type: 'item_completed',
    thread_id: 't1',
    turn_id: 'u1',
    item: body,
    started_at_ms: 1,
    completed_at_ms: 2,
  });

export const sessionMeta = (): Rec =>
  record('session_meta', {
    id: 't1',
    cwd: '/work/proj',
    cli_version: '0.163.0',
    base_instructions: { text: 'x'.repeat(70_000) },
  });

export const userItem = (text: string): Rec =>
  item({ type: 'UserMessage', id: 'i-u', content: [{ type: 'text', text, text_elements: [] }] });

export const agentItem = (text: string, phase = 'final_answer'): Rec =>
  item({ type: 'AgentMessage', id: 'i-a', content: [{ type: 'Text', text }], phase });

export const BIG_OUTPUT = `${'build output row\n'.repeat(3000)}tail-marker`;

export const commandItem = (over: Rec = {}): Rec =>
  item({
    type: 'CommandExecution',
    id: 'cmd1',
    process_id: '1',
    command: ['/bin/zsh', '-lc', 'git status --short'],
    cwd: 'file:///work/proj',
    parsed_cmd: [{ type: 'unknown', cmd: 'git status --short' }],
    source: 'agent',
    status: 'completed',
    aggregated_output: ' M a.ts\n',
    exit_code: 0,
    duration: { secs: 0, nanos: 1000 },
    ...over,
  });

/** A rollout whose response_item records duplicate the items, as Codex writes it. */
export const codexRollout = (): Rec[] => [
  sessionMeta(),
  record('turn_context', {
    cwd: '/work/proj',
    model: 'gpt',
    collaboration_mode: { mode: 'default' },
  }),
  record('event_msg', { type: 'task_started', turn_id: 'u1' }),
  record('response_item', {
    type: 'message',
    role: 'user',
    content: [{ type: 'input_text', text: '# AGENTS.md instructions for /work/proj' }],
  }),
  record('response_item', {
    type: 'message',
    role: 'user',
    content: [{ type: 'input_text', text: 'list the changes' }],
  }),
  userItem('list the changes'),
  item({ type: 'Reasoning', id: 'r0', summary_text: [], raw_content: [] }),
  commandItem({ aggregated_output: BIG_OUTPUT }),
  record('response_item', {
    type: 'custom_tool_call',
    name: 'exec',
    input: 'const r = await tools.exec_command({cmd:"git status --short"})',
  }),
  agentItem('There is one change.'),
  record('event_msg', { type: 'token_count', info: null }),
  record('event_msg', { type: 'task_complete', turn_id: 'u1', last_agent_message: 'done' }),
];
