import type { SessionOutput, SessionPermission } from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';
import { onLive } from '../../../lib/live.ts';
import type { RuntimeState } from '../model.ts';
import { isTurnState } from '../runtime.ts';
import {
  addPermission,
  applyOutput,
  expirePermissions,
  type LiveTurn,
  type OpenPermission,
  type PlanIntent,
  resolvePlan,
  SETTLE_MAX_MS,
  SETTLE_QUIET_MS,
  SETTLE_QUIET_PROMPT_ONLY_MS,
  withPlans,
} from './live-turn.ts';
import type { CardRules } from './live-view.tsx';
import type { PlanView } from './plan-card.tsx';
import type { ReaderSnapshot, SessionReader } from './reader.ts';

export type LiveFixture = {
  turn: LiveTurn | null;
  permissions?: OpenPermission[];
  /** What the plan card starts with: its mode menu open, or the feedback field filled. */
  plan?: PlanView;
  /** What the permission card opens its rule draft with. */
  draft?: NonNullable<CardRules['open']>;
};

let counter = 0;
function newTurn(init: Omit<LiveTurn, 'id' | 'rows' | 'phase'>): LiveTurn {
  return { ...init, id: `live-${++counter}`, rows: [], phase: 'live' };
}

/**
 * The live turn of the selected session: it begins when a prompt is sent from this window or the session
 * starts a turn, collects streamed output and permission requests, and settles into the stored lines.
 * While it lives, stored lines from its boundary on stay hidden, so the streamed output is replaced by them
 * in one step when they have arrived (docs/spec.md#remote-control, step 6).
 */
export function useLiveTurn(opts: {
  sessionId: string;
  state: RuntimeState;
  error: string | undefined;
  snapshot: ReaderSnapshot;
  reader: SessionReader | null;
  fixture?: LiveFixture | undefined;
}) {
  const { sessionId, state, error, snapshot, reader, fixture } = opts;
  const [turn, setTurn] = useState<LiveTurn | null>(fixture?.turn ?? null);
  const [permissions, setPermissions] = useState<OpenPermission[]>(fixture?.permissions ?? []);
  const open = useRef(permissions);
  open.current = permissions;
  const intents = useRef(new Map<string, PlanIntent>());
  const snap = useRef(snapshot);
  snap.current = snapshot;
  const previous = useRef<RuntimeState | null>(null);
  const changedAt = useRef(0);
  const locked = fixture !== undefined;

  // Streamed output and permission requests of this session.
  useEffect(() => {
    if (locked) return;
    return onLive((event) => {
      if (event.kind !== 'message') return;
      const msg = event.msg;
      if (msg.type === 'session.output' && msg.sessionId === sessionId) {
        const output: SessionOutput = msg;
        setTurn((t) => {
          const base =
            t && t.phase === 'live'
              ? t
              : newTurn({
                  prompt: null,
                  startedAt: Date.now(),
                  boundary: snap.current.total,
                  joined: false,
                  reachedRunning: true,
                });
          return { ...base, rows: applyOutput(base.rows, output, Date.now()) };
        });
      } else if (msg.type === 'session.permission' && msg.sessionId === sessionId) {
        const request: SessionPermission = msg;
        setPermissions((list) => addPermission(list, request, Date.now()));
      }
    });
  }, [sessionId, locked]);

  // The runtime state moves the turn along. Each state the window is told is applied in order (below, from the
  // socket message itself): states that arrive within one render, such as running and ready of a short turn,
  // would otherwise reach this hook as ready after ready, and the turn would never settle.
  // Closing requests: a plan leaves a row behind that names the decision this window made, if any.
  const close = useCallback((ids: string[] | 'all') => {
    const gone = open.current.filter((p) => ids === 'all' || ids.includes(p.requestId));
    if (gone.length === 0) return;
    const now = Date.now();
    const plans = gone.flatMap((p) => {
      const row = resolvePlan(p, intents.current.get(p.requestId), now);
      intents.current.delete(p.requestId);
      return row ? [row] : [];
    });
    open.current = open.current.filter((p) => !gone.includes(p));
    setPermissions((list) => list.filter((p) => !gone.some((g) => g.requestId === p.requestId)));
    if (plans.length > 0) setTurn((t) => (t ? withPlans(t, plans) : t));
  }, []);
  const ready = snapshot.ready;
  const apply = useCallback(
    (next: RuntimeState, failure: string | undefined) => {
      const before = previous.current;
      previous.current = next;
      if (before === next) return;
      // A request can reach the window before the state that announces it: only leaving the waiting state
      // closes the requests.
      if (before === 'waiting-permission' && next !== 'waiting-permission') close('all');
      setTurn((t) => {
        if (isTurnState(next)) {
          if (t && t.phase === 'live') {
            return next === 'starting' || t.reachedRunning ? t : { ...t, reachedRunning: true };
          }
          return newTurn({
            prompt: null,
            startedAt: Date.now(),
            boundary: snap.current.total,
            joined: before === null,
            reachedRunning: next !== 'starting',
          });
        }
        if (t?.phase !== 'live') return t;
        if (next === 'failed') {
          // A start that failed leaves no turn behind; a turn that ran and failed stays, with its error.
          return t.reachedRunning
            ? { ...t, phase: 'failed', endedAt: Date.now(), ...(failure ? { error: failure } : {}) }
            : null;
        }
        return { ...t, phase: 'settling', endedAt: Date.now() };
      });
    },
    [close],
  );
  const readyRef = useRef(ready);
  readyRef.current = ready;
  useEffect(() => {
    if (locked || !ready) return;
    apply(state, error);
  }, [state, error, ready, locked, apply]);
  useEffect(() => {
    if (locked) return;
    return onLive((event) => {
      if (event.kind !== 'message') return;
      const msg = event.msg;
      if (msg.type === 'session.runtime' && msg.sessionId === sessionId && readyRef.current)
        apply(msg.state, msg.error);
    });
  }, [sessionId, locked, apply]);

  // Expired permission requests close themselves; the agent denies them at the same time.
  const hasPermissions = permissions.length > 0;
  useEffect(() => {
    if (!hasPermissions || locked) return;
    const tick = setInterval(
      () => setPermissions((list) => expirePermissions(list, Date.now())),
      1000,
    );
    return () => clearInterval(tick);
  }, [hasPermissions, locked]);

  // Settling: the stored lines are on their way; hand over when they have arrived and are quiet.
  const end = snapshot.end;
  // biome-ignore lint/correctness/useExhaustiveDependencies: a changed end is what is being noted
  useEffect(() => {
    changedAt.current = Date.now();
  }, [end]);
  const settling = turn?.phase === 'settling' ? turn : null;
  useEffect(() => {
    if (!settling || locked) return;
    void reader?.pull();
    const poll = setInterval(() => void reader?.pull(), 1000);
    return () => clearInterval(poll);
  }, [settling, reader, locked]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: a changed end restarts the quiet wait
  useEffect(() => {
    if (!settling || locked) return;
    const stored = snapshot.entries.filter((e) => e.lineNo >= settling.boundary);
    const hasAssistant = stored.some((e) => e.role === 'assistant');
    const hasUser = stored.some((e) => e.role === 'user');
    const quiet = hasAssistant
      ? SETTLE_QUIET_MS
      : hasUser
        ? SETTLE_QUIET_PROMPT_ONLY_MS
        : SETTLE_MAX_MS;
    const since = settling.endedAt ?? Date.now();
    const due = Math.min(since + SETTLE_MAX_MS, Math.max(since, changedAt.current) + quiet);
    const timer = setTimeout(
      () => setTurn((t) => (t && t.id === settling.id ? null : t)),
      Math.max(0, due - Date.now()),
    );
    return () => clearTimeout(timer);
  }, [settling, snapshot.entries, snapshot.end, locked]);

  /** A prompt was sent from this window: its turn is live from now. */
  const begin = useCallback((prompt: string) => {
    intents.current.clear();
    setPermissions([]);
    setTurn(
      newTurn({
        prompt,
        startedAt: Date.now(),
        boundary: snap.current.total,
        joined: false,
        reachedRunning: false,
      }),
    );
  }, []);

  /** The prompt was refused: the turn never was. */
  const cancel = useCallback(() => setTurn((t) => (t?.phase === 'live' ? null : t)), []);

  const answered = useCallback((requestId: string) => close([requestId]), [close]);
  /** Notes what this window is about to send for a plan, so the row it leaves can name it. */
  const intend = useCallback((requestId: string, intent: PlanIntent) => {
    intents.current.set(requestId, intent);
  }, []);
  const forget = useCallback((requestId: string) => {
    intents.current.delete(requestId);
  }, []);

  return { turn, permissions, begin, cancel, answered, intend, forget };
}
