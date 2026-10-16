import type { Queue } from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchQueue } from '../../../lib/api.ts';
import { onLive } from '../../../lib/live.ts';
import { EMPTY_QUEUE } from './model.ts';

/**
 * The session's queue as the core holds it (ADR 0020): read on mount, again after every `session.queue` of this
 * session and after the link comes back; a mutation's answer is the newest state. Windows stay in step through
 * the core's broadcast. A `fixture` replaces the reads.
 */
export function useQueue(
  sessionId: string,
  fixture?: Queue,
): { queue: Queue; apply: (queue: Queue) => void; refresh: () => void } {
  const [queue, setQueue] = useState<Queue>(fixture ?? EMPTY_QUEUE);
  const seq = useRef(0);

  const apply = useCallback((next: Queue) => {
    seq.current++;
    setQueue(next);
  }, []);

  const refresh = useCallback(() => {
    if (fixture) return;
    const mine = ++seq.current;
    fetchQueue(sessionId).then(
      (next) => {
        if (seq.current === mine) setQueue(next);
      },
      () => {
        // the next broadcast or reconnect reads again
      },
    );
  }, [sessionId, fixture]);

  useEffect(() => {
    if (fixture) {
      setQueue(fixture);
      return;
    }
    setQueue(EMPTY_QUEUE);
    refresh();
    return onLive((event) => {
      if (event.kind === 'open') refresh();
      else if (
        event.kind === 'message' &&
        event.msg.type === 'session.queue' &&
        event.msg.sessionId === sessionId
      ) {
        refresh();
      }
    });
  }, [sessionId, fixture, refresh]);

  return { queue, apply, refresh };
}
