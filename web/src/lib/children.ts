// The logic of the child session views (ADR 0035, issue #81): the Children tab of the inspector and the
// permission strips of the parent's turn. Pure; the hook that reads the core is use-children.ts.
import type {
  ChildState,
  ChildStatus,
  RuntimeState,
  SessionChildrenResponse,
  SessionDescendant,
  SessionPermission,
  ToolKind,
} from '@hoplet/shared';
import { t } from './i18n.ts';
import { fill } from './templates.ts';

/** One descendant of the session being viewed, as the views show it. */
export type Kid = {
  id: string;
  /** The session that started it; the viewed session for a direct child. */
  parentId: string | null;
  /** 1 for a child of the viewed session, 2 for a child of a child. */
  depth: number;
  title: string;
  machine: string;
  branch: string | null;
  state: ChildState;
  /** Seconds since the core started it; only the core's status of a direct child tells. */
  elapsedSeconds: number | null;
  /** The session started children of its own. */
  childCount: number;
};

/** What the views hold: the tree of the viewed session, as the core last told it. */
export type KidsData = {
  kids: Kid[];
  /** More descendants exist than the list holds. */
  truncated: boolean;
};

export const NO_KIDS: KidsData = { kids: [], truncated: false };

/** A child still at work: it can be cancelled, and a stop of its parent can reach it. */
export function isLive(state: ChildState): boolean {
  return state === 'running' || state === 'blocked';
}

/**
 * Where a session stands for its parent. The core states a direct child's own (`status`: it knows a timeout
 * and a cancel); a deeper session, or one the status did not name, is read from its runtime state.
 */
export function stateOfKid(runtime: RuntimeState, status?: ChildStatus): ChildState {
  if (status) return status.state;
  switch (runtime) {
    case 'starting':
    case 'running':
      return 'running';
    case 'waiting-permission':
      return 'blocked';
    case 'failed':
      return 'failed';
    case 'offline':
      return 'ended';
    default:
      return 'done';
  }
}

function titleOf(s: SessionDescendant): string {
  return s.titleOverride?.trim() || s.title || t('Untitled session');
}

/** The tree of the response as rows, the pre-order of the core kept; `statuses` are the direct children's. */
export function buildKids(
  response: Pick<SessionChildrenResponse, 'descendants' | 'truncated'>,
  statuses: readonly ChildStatus[],
): KidsData {
  const byId = new Map(statuses.map((s) => [s.sessionId, s]));
  const kids = response.descendants.map((d): Kid => {
    const status = byId.get(d.sessionId);
    return {
      id: d.sessionId,
      parentId: d.parentId,
      depth: d.depth,
      title: titleOf(d),
      machine: d.deviceName,
      branch: d.gitBranch ?? null,
      state: stateOfKid(d.runtime.state, status),
      elapsedSeconds: status ? status.elapsedSeconds : null,
      childCount: d.childCount,
    };
  });
  return { kids, truncated: response.truncated };
}

export type Rollup = Record<ChildState, number>;

export function rollup(kids: readonly Kid[]): Rollup {
  const count: Rollup = {
    running: 0,
    done: 0,
    failed: 0,
    blocked: 0,
    ended: 0,
    'timed-out': 0,
    cancelled: 0,
  };
  for (const kid of kids) count[kid.state] += 1;
  return count;
}

/** The roll-up of the tab, in the order the design reads: what needs the user, what runs, then how the rest ended. */
export function rollTags(count: Rollup): { state: ChildState; text: string }[] {
  const tag = (state: ChildState, text: string) =>
    count[state] > 0 ? [{ state, text: fill(text, { count: String(count[state]) }) }] : [];
  return [
    ...tag('blocked', t('{count} needs you')),
    ...tag('running', t('{count} running')),
    ...tag('done', t('{count} done')),
    ...tag('failed', t('{count} failed')),
    ...tag('timed-out', t('{count} timed out')),
    ...tag('cancelled', t('{count} cancelled')),
    ...tag('ended', t('{count} ended')),
  ];
}

export function stateLabel(state: ChildState): string {
  return {
    running: t('Running'),
    blocked: t('Needs you'),
    done: t('Done'),
    failed: t('Failed'),
    ended: t('Ended'),
    'timed-out': t('Timed out'),
    cancelled: t('Cancelled'),
  }[state];
}

/** The sessions a cancel of `id` with its subtree reaches: the session and every live one below it. */
export function subtreeOf(kids: readonly Kid[], id: string): Kid[] {
  const root = kids.find((k) => k.id === id);
  if (!root) return [];
  const reached = new Set([id]);
  const out: Kid[] = [root];
  // the list is in pre-order: a parent comes before its children
  for (const kid of kids) {
    if (kid.parentId !== null && reached.has(kid.parentId) && !reached.has(kid.id)) {
      reached.add(kid.id);
      if (isLive(kid.state)) out.push(kid);
    }
  }
  return out;
}

/** What cancelling a child asks of the core: the sessions it stops, and whether the subtree flag is needed. */
export function cancelPlan(kids: readonly Kid[], id: string): { targets: Kid[]; subtree: boolean } {
  const targets = subtreeOf(kids, id);
  return { targets, subtree: targets.length > 1 };
}

/** The direct children "Cancel all" cancels (each with its subtree): those with a live session at or below them. */
export function cancelAllRoots(kids: readonly Kid[], parentId: string): Kid[] {
  return kids.filter(
    (k) => k.parentId === parentId && subtreeOf(kids, k.id).some((s) => isLive(s.state)),
  );
}

/** The live sessions in the tree: what a cascading stop of the viewed session also stops. */
export function liveKids(kids: readonly Kid[]): Kid[] {
  return kids.filter((k) => isLive(k.state));
}

/** Token count of a collected child's usage: what it read plus what it wrote. */
export function usedTokens(usage: {
  input: number;
  output: number;
  cacheWrite: number;
  cacheRead: number;
}): number {
  return usage.input + usage.output + usage.cacheWrite + usage.cacheRead;
}

/** An open permission request of a child, as the strip under its row shows it. */
export type ChildAsk = {
  requestId: string;
  tool: string;
  toolKind: ToolKind;
  input: unknown;
  /** A plan to approve: its modes and feedback are answered in the child's own conversation, not here. */
  plan: boolean;
};

type AskAsks = Record<string, ChildAsk[]>;

/** What a request asks, in one line: the command of a shell call, else the tool's name. */
export function askLine(ask: Pick<ChildAsk, 'tool' | 'toolKind' | 'input'>): {
  text: string;
  command: boolean;
} {
  const input = ask.input;
  if (ask.toolKind === 'run' && input && typeof input === 'object') {
    const command = (input as Record<string, unknown>).command;
    if (typeof command === 'string' && command.trim() !== '') {
      const first = command.trim().split('\n')[0] ?? '';
      return { text: first.length > 120 ? `${first.slice(0, 119)}…` : first, command: true };
    }
  }
  return { text: ask.tool, command: false };
}

/** Adds a request a child streamed; one already held (the core replays on every subscription) is not added twice. */
export function addAsk(
  asks: AskAsks,
  msg: Pick<
    SessionPermission,
    'sessionId' | 'requestId' | 'tool' | 'toolKind' | 'input' | 'planExit'
  >,
): AskAsks {
  const list = asks[msg.sessionId] ?? [];
  if (list.some((a) => a.requestId === msg.requestId)) return asks;
  const ask: ChildAsk = {
    requestId: msg.requestId,
    tool: msg.tool,
    toolKind: msg.toolKind ?? 'other',
    input: msg.input,
    plan: msg.planExit !== undefined,
  };
  return { ...asks, [msg.sessionId]: [...list, ask] };
}

/** Keeps the requests of the children in `keep` (the blocked ones); the others moved on. */
export function pruneAsks(asks: AskAsks, keep: ReadonlySet<string>): AskAsks {
  const ids = Object.keys(asks);
  if (ids.every((id) => keep.has(id))) return asks;
  return Object.fromEntries(ids.filter((id) => keep.has(id)).map((id) => [id, asks[id] ?? []]));
}

/** Drops one answered request. */
export function dropAsk(asks: AskAsks, childId: string, requestId: string): AskAsks {
  const list = asks[childId];
  if (!list?.some((a) => a.requestId === requestId)) return asks;
  const rest = list.filter((a) => a.requestId !== requestId);
  const { [childId]: _gone, ...others } = asks;
  return rest.length > 0 ? { ...others, [childId]: rest } : others;
}
