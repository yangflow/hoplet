import type { SessionOutput, SessionPermission } from '@hoplet/shared';
import { PROTOCOL_VERSION } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  formatBytes,
  formatCountdown,
  formatDuration,
  formatTimer,
} from '../src/screens/cockpit/conversation/format.ts';
import {
  addPermission,
  applyOutput,
  expirePermissions,
  type LiveRow,
  MAX_LIVE_ROWS,
  PERMISSION_OPEN_MS,
  permissionInput,
} from '../src/screens/cockpit/conversation/live-turn.ts';
import { composerView, startErrorName } from '../src/screens/cockpit/runtime.ts';

const out = (over: Partial<SessionOutput> & Pick<SessionOutput, 'kind'>): SessionOutput => ({
  v: PROTOCOL_VERSION,
  type: 'session.output',
  sessionId: 's',
  ...over,
});

describe('applyOutput', () => {
  it('streams text into one row and thinking tokens into one count', () => {
    let rows: LiveRow[] = [];
    rows = applyOutput(rows, out({ kind: 'thinking', tokens: 300 }), 0);
    rows = applyOutput(rows, out({ kind: 'thinking', tokens: 900 }), 0);
    rows = applyOutput(rows, out({ kind: 'text', text: 'One ' }), 0);
    rows = applyOutput(rows, out({ kind: 'text', text: 'test fails' }), 0);
    expect(rows).toMatchObject([
      { kind: 'thinking', tokens: 1200 },
      { kind: 'text', text: 'One test fails' },
    ]);
  });

  it('finishes a tool row with its result by id', () => {
    let rows: LiveRow[] = [];
    rows = applyOutput(
      rows,
      out({
        kind: 'tool-call',
        tool: 'Bash',
        toolKind: 'run',
        toolUseId: 'a',
        input: { command: 'x' },
      }),
      1000,
    );
    rows = applyOutput(
      rows,
      out({ kind: 'tool-call', tool: 'Bash', toolUseId: 'b', input: { command: 'y' } }),
      1100,
    );
    rows = applyOutput(
      rows,
      out({ kind: 'tool-result', toolUseId: 'b', text: 'boom', isError: true }),
      3000,
    );
    expect(rows[0]).toMatchObject({ kind: 'tool', toolUseId: 'a', toolKind: 'run' });
    expect(rows[1]).toMatchObject({ toolKind: 'other' });
    expect((rows[0] as { result?: unknown }).result).toBeUndefined();
    expect(rows[1]).toMatchObject({
      endedAt: 3000,
      result: { text: 'boom', failed: true, bytes: 4 },
    });
  });

  it('keeps a result with no call as its own row and bounds the rows', () => {
    let rows = applyOutput([], out({ kind: 'tool-result', toolUseId: 'zz', text: 'late' }), 0);
    expect(rows).toMatchObject([{ kind: 'result', text: 'late' }]);
    for (let i = 0; i < MAX_LIVE_ROWS + 50; i++) {
      rows = applyOutput(rows, out({ kind: 'tool-call', tool: 'T', toolUseId: String(i) }), 0);
    }
    expect(rows).toHaveLength(MAX_LIVE_ROWS);
  });
});

describe('permission requests', () => {
  const request = (id: string): SessionPermission => ({
    v: PROTOCOL_VERSION,
    type: 'session.permission',
    sessionId: 's',
    requestId: id,
    tool: 'Bash',
    toolKind: 'run',
    input: { command: 'pnpm check' },
  });

  it('keeps each request once and closes it after ten minutes', () => {
    let list = addPermission([], request('a'), 1000);
    list = addPermission(list, request('a'), 2000);
    list = addPermission(list, request('b'), 5000);
    expect(list.map((p) => p.requestId)).toEqual(['a', 'b']);
    expect(expirePermissions(list, 1000 + PERMISSION_OPEN_MS).map((p) => p.requestId)).toEqual([
      'b',
    ]);
    expect(expirePermissions(list, 1000)).toBe(list);
  });

  it('shows the command of a tool that runs one and the JSON of the others', () => {
    expect(addPermission([], request('a'), 0)[0]?.toolKind).toBe('run');
    expect(permissionInput('run', { command: 'ls -la' })).toEqual({
      text: 'ls -la',
      command: true,
    });
    expect(permissionInput('write', { path: 'a', command: 'x' })).toEqual({
      text: '{\n  "path": "a",\n  "command": "x"\n}',
      command: false,
    });
  });
});

describe('composerView', () => {
  const base = {
    ownerConnected: true,
    live: true,
    deviceName: 'MacBook Pro',
    providerName: 'Claude Code',
    turnFailed: false,
  };

  it('shows nothing for an idle session', () => {
    const view = composerView({ ...base, state: 'idle' });
    expect(view).toMatchObject({
      layout: 'field',
      inputEnabled: true,
      placeholder: 'Ask Claude Code…',
    });
    expect(view.line).toBeUndefined();
    expect(view.action).toBeUndefined();
  });

  it('says why input is off in a few words', () => {
    const placeholder = (state: Parameters<typeof composerView>[0]['state'], over = {}) =>
      composerView({ ...base, state, ...over }).placeholder;
    expect(placeholder('starting')).toBe('Starting Claude Code');
    expect(placeholder('running')).toBe('Turn running');
    expect(placeholder('waiting-permission')).toBe('Waiting for permission');
    expect(placeholder('running', { live: false })).toBe('Reconnecting');
    expect(composerView({ ...base, state: 'running' }).inputEnabled).toBe(false);
  });

  it('names the session states that change what the user can do', () => {
    expect(composerView({ ...base, state: 'offline' })).toMatchObject({
      layout: 'bar',
      action: 'handoff',
      line: { text: 'MacBook Pro offline' },
    });
    expect(composerView({ ...base, ownerConnected: false, state: 'idle' }).action).toBe('handoff');
    expect(composerView({ ...base, state: 'external' })).toMatchObject({
      layout: 'bar',
      line: { text: 'Read only' },
    });
    expect(composerView({ ...base, state: 'ready' })).toMatchObject({
      action: 'stop',
      inputEnabled: true,
    });
  });

  it('reports a failed start with a named error and a retry, and a failed turn with a way to resend', () => {
    const start = composerView({ ...base, state: 'failed', error: 'claude not found in PATH' });
    expect(start).toMatchObject({
      action: 'retry',
      line: { text: 'Claude Code not found', tone: 'bad' },
    });
    expect(start.line?.title).toContain('claude not found in PATH');
    expect(startErrorName('spawn failed', 'Claude Code')).toBe('Failed to start');
    const turn = composerView({ ...base, state: 'failed', turnFailed: true });
    expect(turn).toMatchObject({ placeholder: 'Send again to retry', inputEnabled: true });
    expect(turn.line).toBeUndefined();
  });
});

describe('format', () => {
  it('writes sizes, durations and timers as the design does', () => {
    expect(formatBytes(6246)).toBe('6.1 KiB');
    expect(formatBytes(412 * 1024)).toBe('412 KiB');
    expect(formatBytes(3 * 1024 * 1024)).toBe('3.0 MiB');
    expect(formatDuration(68_000)).toBe('1m 08s');
    expect(formatDuration(21_000)).toBe('21s');
    expect(formatDuration(3_720_000)).toBe('1h 02m');
    expect(formatTimer(48_000)).toBe('00:48');
    expect(formatCountdown(581_000)).toBe('9:41');
  });
});
