import type { SessionHandoffRequest } from '@hoplet/shared';
import { useCallback, useEffect, useState } from 'react';
import { fetchHandoffRequests, postHandoffRequestDecision } from './api.ts';
import { onLive } from './live.ts';

/** The request with its newer state in place, the newest request first. */
export function withRequest(
  list: readonly SessionHandoffRequest[],
  next: SessionHandoffRequest,
): SessionHandoffRequest[] {
  const rest = list.filter((r) => r.requestId !== next.requestId);
  return [next, ...rest].sort((a, b) => b.createdAt - a.createdAt);
}

/**
 * A session's hand-off requests (ADR 0034): loaded once, then kept current by the core's `handoff.request`
 * messages. A fixture replaces both. `decide` resolves to an error message, or null when it was recorded.
 */
export function useHandoffRequests(sessionId: string, fixture?: readonly SessionHandoffRequest[]) {
  const [requests, setRequests] = useState<SessionHandoffRequest[]>(() => [...(fixture ?? [])]);
  const [deciding, setDeciding] = useState(false);
  const locked = fixture !== undefined;

  useEffect(() => {
    if (locked) return;
    const abort = new AbortController();
    const load = () => {
      void fetchHandoffRequests(sessionId, abort.signal)
        .then((r) => setRequests(r.requests))
        .catch(() => {});
    };
    load();
    const off = onLive((event) => {
      if (event.kind === 'open') load();
      else if (
        event.kind === 'message' &&
        event.msg.type === 'handoff.request' &&
        event.msg.request.sessionId === sessionId
      ) {
        const next = event.msg.request;
        setRequests((cur) => withRequest(cur, next));
      }
    });
    return () => {
      abort.abort();
      off();
    };
  }, [sessionId, locked]);

  const decide = useCallback(
    async (requestId: string, approve: boolean): Promise<string | null> => {
      setDeciding(true);
      try {
        const next = await postHandoffRequestDecision(requestId, approve);
        setRequests((cur) => withRequest(cur, next));
        return null;
      } catch (err) {
        // A request that was closed meanwhile is shown as it is now.
        void fetchHandoffRequests(sessionId)
          .then((r) => setRequests(r.requests))
          .catch(() => {});
        return err instanceof Error ? err.message : 'failed';
      } finally {
        setDeciding(false);
      }
    },
    [sessionId],
  );

  return { requests, decide, deciding };
}
