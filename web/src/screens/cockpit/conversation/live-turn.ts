/**
 * The live turn: what streams while a turn runs. Runtime output is transient (docs/spec.md#remote-control);
 * the stored lines replace it when they arrive.
 */
import type { SessionOutput, SessionPermission, ToolKind, ToolMcp } from '@hoplet/shared';

export type LiveRow =
  | { id: number; kind: 'thinking'; tokens: number }
  | { id: number; kind: 'text'; text: string }
  | {
      id: number;
      kind: 'tool';
      toolUseId?: string;
      tool: string;
      toolKind: ToolKind;
      mcp?: ToolMcp;
      input: unknown;
      startedAt: number;
      endedAt?: number;
      result?: { text: string; failed: boolean; bytes: number };
    }
  | { id: number; kind: 'result'; text: string; failed: boolean; bytes: number };

export type OpenPermission = {
  requestId: string;
  tool: string;
  toolKind: ToolKind;
  toolUseId?: string;
  input: unknown;
  receivedAt: number;
};

/** A request nobody answers is denied after this long (docs/spec.md#remote-control). */
export const PERMISSION_OPEN_MS = 10 * 60 * 1000;
/** Rows of streamed output kept; older ones are in the stored lines by then. */
export const MAX_LIVE_ROWS = 400;

export type LiveTurn = {
  id: string;
  /** The prompt sent from this window; null when the turn started elsewhere. */
  prompt: string | null;
  startedAt: number;
  /** Stored lines from here on belong to the live turn and stay hidden until it settles. */
  boundary: number;
  /** The turn was running when this window opened: the stored lines before the boundary are its first rows. */
  joined: boolean;
  /** The turn got past `starting`: a failure is the turn's, not the start's. */
  reachedRunning: boolean;
  rows: LiveRow[];
  phase: 'live' | 'failed' | 'settling';
  /** When the turn stopped being live: freezes its timer. */
  endedAt?: number;
  error?: string;
};

function nextId(rows: LiveRow[]): number {
  return (rows[rows.length - 1]?.id ?? -1) + 1;
}

function bounded(rows: LiveRow[]): LiveRow[] {
  return rows.length > MAX_LIVE_ROWS ? rows.slice(rows.length - MAX_LIVE_ROWS) : rows;
}

/** Folds one `session.output` into the rows: text and thinking extend the last row of their kind. */
export function applyOutput(rows: LiveRow[], msg: SessionOutput, now: number): LiveRow[] {
  const last = rows[rows.length - 1];
  switch (msg.kind) {
    case 'text': {
      const text = msg.text ?? '';
      if (last?.kind === 'text') return [...rows.slice(0, -1), { ...last, text: last.text + text }];
      return bounded([...rows, { id: nextId(rows), kind: 'text', text }]);
    }
    case 'thinking': {
      const tokens = msg.tokens ?? 0;
      if (last?.kind === 'thinking') {
        return [...rows.slice(0, -1), { ...last, tokens: last.tokens + tokens }];
      }
      return bounded([...rows, { id: nextId(rows), kind: 'thinking', tokens }]);
    }
    case 'tool-call':
      return bounded([
        ...rows,
        {
          id: nextId(rows),
          kind: 'tool',
          ...(msg.toolUseId !== undefined ? { toolUseId: msg.toolUseId } : {}),
          tool: msg.tool ?? '',
          toolKind: msg.toolKind ?? 'other',
          ...(msg.mcp ? { mcp: msg.mcp } : {}),
          input: msg.input,
          startedAt: now,
        },
      ]);
    case 'tool-result': {
      const text = msg.text ?? '';
      const result = { text, failed: msg.isError === true, bytes: text.length };
      const index = rows.findIndex(
        (r) =>
          r.kind === 'tool' &&
          r.result === undefined &&
          (msg.toolUseId === undefined || r.toolUseId === msg.toolUseId),
      );
      const target = rows[index];
      if (index >= 0 && target?.kind === 'tool') {
        const next = rows.slice();
        next[index] = { ...target, endedAt: now, result };
        return next;
      }
      return bounded([...rows, { id: nextId(rows), kind: 'result', ...result }]);
    }
  }
}

export function addPermission(
  list: OpenPermission[],
  msg: SessionPermission,
  now: number,
): OpenPermission[] {
  if (list.some((p) => p.requestId === msg.requestId)) return list;
  return [
    ...list,
    {
      requestId: msg.requestId,
      tool: msg.tool,
      toolKind: msg.toolKind ?? 'other',
      ...(msg.toolUseId !== undefined ? { toolUseId: msg.toolUseId } : {}),
      input: msg.input,
      receivedAt: now,
    },
  ];
}

export function expirePermissions(list: OpenPermission[], now: number): OpenPermission[] {
  const open = list.filter((p) => now - p.receivedAt < PERMISSION_OPEN_MS);
  return open.length === list.length ? list : open;
}

/** What a permission request shows of its input: the command of a tool that runs one, otherwise the JSON. */
export function permissionInput(
  kind: ToolKind,
  input: unknown,
): { text: string; command: boolean } {
  if (input && typeof input === 'object') {
    const command = (input as Record<string, unknown>).command;
    if (typeof command === 'string' && kind === 'run') {
      return { text: command, command: true };
    }
  }
  const text = typeof input === 'string' ? input : JSON.stringify(input ?? null, null, 2);
  return { text, command: false };
}

/** Input above this is elided like any block. */
export const PERMISSION_ELIDE_BYTES = 8 * 1024;
export const ELIDED_PREVIEW_CHARS = 1024;

/** How long a stored turn takes to arrive: the live turn waits for it at most this long. */
export const SETTLE_MAX_MS = 5000;
export const SETTLE_QUIET_MS = 500;
export const SETTLE_QUIET_PROMPT_ONLY_MS = 1500;
