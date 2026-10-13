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
  SETTLE_MAX_MS,
  SETTLE_QUIET_MS,
  SETTLE_QUIET_PROMPT_ONLY_MS,
} from './live-turn.ts';
import type { ReaderSnapshot, SessionReader } from './reader.ts';

export type LiveFixture = { turn: LiveTurn | null; permissions?: OpenPermission[] };

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

  // The runtime state moves the turn along.
  const ready = snapshot.ready;
  useEffect(() => {
    if (locked || !ready) return;
    const before = previous.current;
    previous.current = state;
    if (before === state) return;
    // A request can reach the window before the state that announces it: only leaving the waiting state
    // closes the requests.
    if (before === 'waiting-permission' && state !== 'waiting-permission')
      setPermissions((list) => (list.length === 0 ? list : []));
    setTurn((t) => {
      if (isTurnState(state)) {
        if (t && t.phase === 'live') {
          return state === 'starting' || t.reachedRunning ? t : { ...t, reachedRunning: true };
        }
        return newTurn({
          prompt: null,
          startedAt: Date.now(),
          boundary: snap.current.total,
          joined: before === null,
          reachedRunning: state !== 'starting',
        });
      }
      if (t?.phase !== 'live') return t;
      if (state === 'failed') {
        // A start that failed leaves no turn behind; a turn that ran and failed stays, with its error.
        return t.reachedRunning
          ? { ...t, phase: 'failed', endedAt: Date.now(), ...(error ? { error } : {}) }
          : null;
      }
      return { ...t, phase: 'settling', endedAt: Date.now() };
    });
  }, [state, error, ready, locked]);

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

  const answered = useCallback((requestId: string) => {
    setPermissions((list) => list.filter((p) => p.requestId !== requestId));
  }, []);

  return { turn, permissions, begin, cancel, answered };
}
