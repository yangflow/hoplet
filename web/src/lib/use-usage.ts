import type { SessionUsage, UsagePrices, UsageResponse } from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchSessionUsage, fetchUsage, fetchUsagePrices, putUsagePrices } from './api.ts';
import { errorText } from './errors.ts';

/** How often a usage that is still being counted after an upgrade is asked again. */
export const BACKFILL_POLL_MS = 5000;
/** How long a session's usage rests after the conversation changed: a streaming answer is one fetch, not one per line. */
export const SESSION_USAGE_DELAY_MS = 1500;

type Query = Parameters<typeof fetchUsage>[0];

export type Loaded<T> = {
  /** null until the core answered. */
  data: T | null;
  loading: boolean;
  error: string | null;
};

/**
 * One usage query, asked again when it changes and, while the core is still counting existing sessions, every few
 * seconds. The last answer stays while the next one loads. A `fixture` is the answer itself and nothing is fetched.
 */
export function useUsage(query: Query, fixture?: UsageResponse): Loaded<UsageResponse> {
  const [data, setData] = useState<UsageResponse | null>(fixture ?? null);
  const [loading, setLoading] = useState(fixture === undefined);
  const [error, setError] = useState<string | null>(null);
  const locked = fixture !== undefined;
  const key = JSON.stringify(query);
  const backfilling = data?.backfilling === true;
  const [again, setAgain] = useState(0);

  // biome-ignore lint/correctness/useExhaustiveDependencies: `key` is the query; `again` is the poll tick
  useEffect(() => {
    if (locked) return;
    const abort = new AbortController();
    setLoading(true);
    fetchUsage(query, abort.signal)
      .then((r) => {
        setData(r);
        setError(null);
        setLoading(false);
      })
      .catch((e) => {
        if (abort.signal.aborted) return;
        setError(errorText(e));
        setLoading(false);
      });
    return () => abort.abort();
  }, [locked, key, again]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: `again` re-arms the timer after each poll
  useEffect(() => {
    if (locked || !backfilling) return;
    const timer = setTimeout(() => setAgain((n) => n + 1), BACKFILL_POLL_MS);
    return () => clearTimeout(timer);
  }, [locked, backfilling, again]);

  if (locked) return { data: fixture, loading: false, error: null };
  return { data, loading, error };
}

/**
 * One session's usage in total and by model. It is asked when `refresh` changes (the session's line count), after a
 * short rest, so the chip follows the conversation. A `fixture` is the answer itself.
 */
export function useSessionUsage(
  sessionId: string | null,
  refresh: number,
  fixture?: SessionUsage,
): Loaded<SessionUsage> {
  const [data, setData] = useState<SessionUsage | null>(fixture ?? null);
  const [error, setError] = useState<string | null>(null);
  const locked = fixture !== undefined;
  const first = useRef(true);

  // biome-ignore lint/correctness/useExhaustiveDependencies: `refresh` only decides when to ask again
  useEffect(() => {
    if (locked || sessionId === null) return;
    const abort = new AbortController();
    const wait = first.current ? 0 : SESSION_USAGE_DELAY_MS;
    first.current = false;
    const timer = setTimeout(() => {
      fetchSessionUsage(sessionId, abort.signal)
        .then((r) => {
          setData(r);
          setError(null);
        })
        .catch((e) => {
          if (!abort.signal.aborted) setError(errorText(e));
        });
    }, wait);
    return () => {
      clearTimeout(timer);
      abort.abort();
    };
  }, [locked, sessionId, refresh]);

  // Another session starts from nothing, not from the last one's numbers.
  // biome-ignore lint/correctness/useExhaustiveDependencies: `sessionId` is what resets the answer
  useEffect(() => {
    if (locked) return;
    first.current = true;
    setData(null);
    setError(null);
  }, [locked, sessionId]);

  if (locked) return { data: fixture, loading: false, error: null };
  return { data, loading: data === null && error === null, error };
}

export type UsagePricesState = Loaded<UsagePrices> & {
  /** Saves all overrides at once; resolves false and shows the error when the core refused. */
  save: (overrides: UsagePrices['overrides']) => Promise<boolean>;
  saving: boolean;
};

/** The price table and its saving. A `fixture` keeps the edits locally. */
export function useUsagePrices(fixture?: UsagePrices): UsagePricesState {
  const [data, setData] = useState<UsagePrices | null>(fixture ?? null);
  const [loading, setLoading] = useState(fixture === undefined);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const locked = fixture !== undefined;

  useEffect(() => {
    if (locked) return;
    let cancelled = false;
    fetchUsagePrices()
      .then((r) => {
        if (!cancelled) setData(r);
      })
      .catch((e) => {
        if (!cancelled) setError(errorText(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [locked]);

  const save = useCallback(
    async (overrides: UsagePrices['overrides']) => {
      if (locked) {
        setData((d) => (d ? { ...d, overrides } : d));
        return true;
      }
      setSaving(true);
      setError(null);
      try {
        setData(await putUsagePrices({ overrides }));
        return true;
      } catch (e) {
        setError(errorText(e));
        return false;
      } finally {
        setSaving(false);
      }
    },
    [locked],
  );

  return { data, loading, error, save, saving };
}
