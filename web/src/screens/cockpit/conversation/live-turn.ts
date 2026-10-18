/**
 * The live turn: what streams while a turn runs. Runtime output is transient (docs/spec.md#remote-control);
 * the stored lines replace it when they arrive.
 */
import type { PlanExit, SessionOutput, SessionPermission, ToolKind, ToolMcp } from '@hoplet/shared';

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
  /** The driver declared the tool a plan exit: the request shows the plan and offers the modes (ADR 0019). */
  planExit?: PlanExit;
};

/** What the user decided about a plan, kept as a row of the turn once the request has closed. */
export type PlanResolution = {
  requestId: string;
  title: string;
  steps: number;
  bytes: number;
  plan: string;
  /** `elsewhere`: the request closed without a decision from this window (another window, an interrupt). */
  outcome: 'approved' | 'kept' | 'elsewhere';
  /** Approved into a new session (ADR 0025): this session stops and the plan goes on there. */
  fresh?: boolean;
  /** The mode the session is in now: the one chosen on approval, the one it stayed in when kept. */
  mode?: string;
  feedback?: string;
  at: number;
  /** The id of the last row when the request closed: the plan sits after it. */
  afterId?: number;
};

/** What this window sent for a plan, noted before the request so the closing can name it. */
export type PlanIntent = {
  outcome: 'approved' | 'kept';
  fresh?: boolean;
  mode?: string;
  feedback?: string;
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
  /** Plans decided during this turn. */
  plans?: PlanResolution[];
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
      ...(msg.planExit ? { planExit: msg.planExit } : {}),
    },
  ];
}

export function expirePermissions(list: OpenPermission[], now: number): OpenPermission[] {
  // A plan waits for as long as the user takes: it is never denied by the clock.
  const open = list.filter(
    (p) => p.planExit !== undefined || now - p.receivedAt < PERMISSION_OPEN_MS,
  );
  return open.length === list.length ? list : open;
}

/** A plan above this many bytes is collapsed to its first lines. */
export const PLAN_COLLAPSE_BYTES = 4 * 1024;

const ORDERED_ITEM = /^\s{0,3}\d{1,9}[.)]\s+\S/;

/** The plan's title: its first heading, else its first line, without Markdown marks. */
export function planTitle(plan: string): string {
  const lines = plan.split('\n').map((l) => l.trim());
  const heading = lines.find((l) => /^#{1,6}\s+\S/.test(l));
  const raw = heading ?? lines.find((l) => l !== '') ?? '';
  const text = raw
    .replace(/^#{1,6}\s+/, '')
    .replace(/[*_`]/g, '')
    .trim();
  return text.length > 120 ? `${text.slice(0, 119)}…` : text;
}

/** The steps of a plan: its numbered items outside code fences. */
export function planSteps(plan: string): number {
  let fenced = false;
  let n = 0;
  for (const line of plan.split('\n')) {
    if (/^\s{0,3}(`{3,}|~{3,})/.test(line)) fenced = !fenced;
    else if (!fenced && ORDERED_ITEM.test(line)) n++;
  }
  return n;
}

export function planBytes(plan: string): number {
  return new TextEncoder().encode(plan).length;
}

/** The modes an approval may switch to, the driver's default first, named by the session's mode option. */
export function planModes(
  exit: PlanExit,
  option: { options: { value: string }[] } | undefined,
): string[] {
  const known = new Set(option?.options.map((o) => o.value));
  const offered = exit.modes.filter((m) => known.size === 0 || known.has(m));
  const rest = offered.filter((m) => m !== exit.default);
  return offered.includes(exit.default) ? [exit.default, ...rest] : offered;
}

export type LiveItem =
  | { key: string; row: LiveRow; plan?: undefined }
  | { key: string; plan: PlanResolution; row?: undefined };

/** The rows of the turn with its decided plans between them, each after the row that was last when it closed. */
export function liveItems(turn: Pick<LiveTurn, 'rows' | 'plans'>): LiveItem[] {
  const plans = turn.plans ?? [];
  const at = (id: number) =>
    plans
      .filter((p) => (p.afterId ?? -1) === id)
      .map((plan) => ({ key: `plan:${plan.requestId}`, plan }));
  const ids = new Set(turn.rows.map((r) => r.id));
  // A plan that was after a row the buffer has dropped goes first.
  const lost = plans
    .filter((p) => (p.afterId ?? -1) !== -1 && !ids.has(p.afterId as number))
    .map((plan) => ({ key: `plan:${plan.requestId}`, plan }));
  return [
    ...at(-1),
    ...lost,
    ...turn.rows.flatMap((row) => [{ key: String(row.id), row }, ...at(row.id)]),
  ];
}

/** The row a closed plan request leaves: this window's own decision when it made one. */
export function resolvePlan(
  request: OpenPermission,
  intent: PlanIntent | undefined,
  now: number,
): PlanResolution | null {
  const exit = request.planExit;
  if (!exit) return null;
  return {
    requestId: request.requestId,
    title: planTitle(exit.plan),
    steps: planSteps(exit.plan),
    bytes: planBytes(exit.plan),
    plan: exit.plan,
    outcome: intent?.outcome ?? 'elsewhere',
    ...(intent?.fresh ? { fresh: true } : {}),
    ...(intent?.mode !== undefined ? { mode: intent.mode } : {}),
    ...(intent?.feedback !== undefined ? { feedback: intent.feedback } : {}),
    at: now,
  };
}

/** The turn with the resolutions added once each. */
export function withPlans(turn: LiveTurn, plans: PlanResolution[]): LiveTurn {
  const known = new Set((turn.plans ?? []).map((p) => p.requestId));
  const afterId = turn.rows[turn.rows.length - 1]?.id ?? -1;
  const fresh = plans.filter((p) => !known.has(p.requestId)).map((p) => ({ ...p, afterId }));
  return fresh.length === 0 ? turn : { ...turn, plans: [...(turn.plans ?? []), ...fresh] };
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
