import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchChildren, fetchChildStatuses } from './api.ts';
import {
  addAsk,
  buildKids,
  type ChildAsk,
  dropAsk,
  isLive,
  type KidsData,
  NO_KIDS,
  pruneAsks,
} from './children.ts';
import { errorText } from './errors.ts';
import { onLive, subscribeSession } from './live.ts';

/** How often the tree is read again while a child works or the session itself is in a turn. */
export const CHILDREN_POLL_MS = 5000;

/** What a fixture holds instead of the core: the tree, the requests of the blocked children and a failed read. */
export type ChildrenFixture = {
  data: KidsData;
  asks?: Record<string, ChildAsk[]>;
  loading?: boolean;
  error?: string;
};

export type ChildrenView = {
  phase: 'loading' | 'live' | 'error';
  data: KidsData;
  error: string | null;
  /** The open permission requests of the blocked children, by child. */
  asks: Record<string, ChildAsk[]>;
  refresh: () => void;
  /** A request was answered here: it leaves the strip before the core says so. */
  answered: (childId: string, requestId: string) => void;
};

/**
 * The descendants of a session and the permission requests its blocked children wait on (ADR 0035). The tree
 * is read when the session opens and again every few seconds while something works; a blocked child is
 * subscribed to, so its requests arrive as they would in its own conversation. A fixture is the answer itself.
 */
export function useChildren(
  sessionId: string | null,
  options: {
    /** The session is in a turn: a child it starts shows up while it works. */
    active: boolean;
    /** The session has children to read; nothing is asked of the core otherwise. */
    enabled: boolean;
    fixture?: ChildrenFixture | undefined;
  },
): ChildrenView {
  const { active, fixture } = options;
  const locked = fixture !== undefined;
  const reading = !locked && sessionId !== null && options.enabled;
  const [data, setData] = useState<KidsData>(fixture?.data ?? NO_KIDS);
  const [phase, setPhase] = useState<ChildrenView['phase']>(
    fixture ? (fixture.error ? 'error' : fixture.loading ? 'loading' : 'live') : 'loading',
  );
  const [error, setError] = useState<string | null>(fixture?.error ?? null);
  const [asks, setAsks] = useState<Record<string, ChildAsk[]>>(fixture?.asks ?? {});
  const [again, setAgain] = useState(0);
  const refresh = useCallback(() => setAgain((n) => n + 1), []);
  const answered = useCallback(
    (childId: string, requestId: string) => setAsks((a) => dropAsk(a, childId, requestId)),
    [],
  );

  // The session opened, or the tree was asked for again. A turn starting or ending reads it too: a child
  // that was started in that turn appears with it.
  // biome-ignore lint/correctness/useExhaustiveDependencies: `again` is the refresh and `active` the turn
  useEffect(() => {
    if (!reading || sessionId === null) return;
    const abort = new AbortController();
    Promise.all([
      fetchChildren(sessionId, abort.signal),
      fetchChildStatuses(sessionId, abort.signal),
    ])
      .then(([tree, statuses]) => {
        setData(buildKids(tree, statuses.children));
        setPhase('live');
        setError(null);
      })
      .catch((err: unknown) => {
        if (abort.signal.aborted) return;
        setError(errorText(err));
        setPhase((p) => (p === 'live' ? p : 'error'));
      });
    return () => abort.abort();
  }, [sessionId, reading, again, active]);

  // Another session, or one without children: what was read belongs to the one before.
  const shownFor = useRef(sessionId);
  useEffect(() => {
    if (locked || (shownFor.current === sessionId && reading)) return;
    shownFor.current = sessionId;
    setData(NO_KIDS);
    setAsks({});
    setError(null);
    setPhase(reading ? 'loading' : 'live');
  }, [sessionId, reading, locked]);

  const anyLive = data.kids.some((k) => isLive(k.state));
  useEffect(() => {
    if (!reading || !(active || anyLive)) return;
    const timer = setInterval(refresh, CHILDREN_POLL_MS);
    return () => clearInterval(timer);
  }, [reading, active, anyLive, refresh]);

  // The children that wait for a permission: their requests are replayed on subscribing and streamed after.
  const blockedKey = data.kids
    .filter((k) => k.state === 'blocked')
    .map((k) => k.id)
    .join(',');
  const blocked = useRef<ReadonlySet<string>>(new Set());
  useEffect(() => {
    const ids = blockedKey === '' ? [] : blockedKey.split(',');
    blocked.current = new Set(ids);
    if (locked) return;
    setAsks((a) => pruneAsks(a, blocked.current));
    if (ids.length === 0) return;
    const unsubscribe = ids.map((id) => subscribeSession(id));
    const off = onLive((event) => {
      if (event.kind !== 'message') return;
      const msg = event.msg;
      if (msg.type === 'session.permission' && blocked.current.has(msg.sessionId)) {
        setAsks((a) => addAsk(a, msg));
      } else if (
        msg.type === 'session.runtime' &&
        blocked.current.has(msg.sessionId) &&
        msg.state !== 'waiting-permission'
      ) {
        // the child moved on: its state and its requests are read again
        refresh();
      }
    });
    return () => {
      off();
      for (const done of unsubscribe) done();
    };
  }, [blockedKey, locked, refresh]);

  return { phase, data, error, asks, refresh, answered };
}
