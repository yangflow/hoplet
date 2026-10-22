import type { AuditEvent, AuditVerifyResponse } from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchAuditEvents, fetchAuditExport, fetchAuditVerify } from './api.ts';
import { type AuditFilters, downloadBlob, exportFileName, matchesQuery, queryOf } from './audit.ts';
import { errorText } from './errors.ts';

/** Records per request. */
export const AUDIT_PAGE = 100;

export type VerifyState =
  | { status: 'idle' }
  | { status: 'running' }
  | { status: 'done'; result: AuditVerifyResponse }
  | { status: 'error'; message: string };

/** What a fixture holds instead of the core: the whole log, the cursor to show and the verification. */
export type AuditSource = {
  events: AuditEvent[];
  /** The cursor shown under the list; null when the list is complete. */
  next?: number | null;
  verify?: VerifyState;
  loading?: boolean;
  error?: string | null;
  /** The clock of the screen. */
  now?: number;
};

export type AuditState = {
  events: AuditEvent[];
  next: number | null;
  loading: boolean;
  loadingMore: boolean;
  error: string | null;
  /** The clock of the list: the time the filters were read at. */
  now: number;
  loadMore: () => void;
  retry: () => void;
  verify: VerifyState;
  runVerify: () => void;
  exporting: boolean;
  /** Downloads the records the filters ask for; resolves the error text, or null when it worked. */
  exportAs: (format: 'jsonl' | 'csv') => Promise<string | null>;
};

/**
 * The records the filters ask for, newest first, a page at a time, and the two actions on the whole log. A
 * `source` is the answer itself and nothing is fetched.
 */
export function useAudit(filters: AuditFilters, source?: AuditSource): AuditState {
  const locked = source !== undefined;
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [next, setNext] = useState<number | null>(null);
  const [loading, setLoading] = useState(!locked);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [now, setNow] = useState(() => source?.now ?? Date.now());
  const [again, setAgain] = useState(0);
  const [verify, setVerify] = useState<VerifyState>({ status: 'idle' });
  const [exporting, setExporting] = useState(false);
  const queryRef = useRef<Record<string, string>>({});
  const key = JSON.stringify(filters);

  // biome-ignore lint/correctness/useExhaustiveDependencies: `key` is the filters; `again` is the retry
  useEffect(() => {
    if (locked) return;
    const at = Date.now();
    const query = queryOf(filters, at);
    queryRef.current = query;
    const abort = new AbortController();
    setLoading(true);
    setNow(at);
    fetchAuditEvents({ ...query, order: 'desc', limit: String(AUDIT_PAGE) }, abort.signal)
      .then((r) => {
        setEvents(r.events);
        setNext(r.next);
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

  const loadMore = useCallback(() => {
    if (locked || next === null || loadingMore) return;
    setLoadingMore(true);
    fetchAuditEvents({
      ...queryRef.current,
      order: 'desc',
      limit: String(AUDIT_PAGE),
      after: String(next),
    })
      .then((r) => {
        setEvents((list) => [...list, ...r.events]);
        setNext(r.next);
        setError(null);
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setLoadingMore(false));
  }, [locked, next, loadingMore]);

  const runVerify = useCallback(() => {
    if (locked) return;
    setVerify({ status: 'running' });
    fetchAuditVerify()
      .then((result) => setVerify({ status: 'done', result }))
      .catch((e) => setVerify({ status: 'error', message: errorText(e) }));
  }, [locked]);

  const exportAs = useCallback(
    async (format: 'jsonl' | 'csv') => {
      if (locked) return null;
      setExporting(true);
      try {
        const blob = await fetchAuditExport(queryOf(filters, Date.now()), format);
        downloadBlob(exportFileName(new Date(), format), blob);
        return null;
      } catch (e) {
        return errorText(e);
      } finally {
        setExporting(false);
      }
    },
    [locked, filters],
  );

  if (source) {
    const at = source.now ?? now;
    const query = queryOf(filters, at);
    return {
      events: source.events.filter((e) => matchesQuery(e, query)),
      next: source.next ?? null,
      loading: source.loading ?? false,
      loadingMore: false,
      error: source.error ?? null,
      now: at,
      loadMore: () => undefined,
      retry: () => undefined,
      verify: source.verify ?? { status: 'idle' },
      runVerify: () => undefined,
      exporting: false,
      exportAs: async () => null,
    };
  }
  return {
    events,
    next,
    loading,
    loadingMore,
    error,
    now,
    loadMore,
    retry: () => setAgain((n) => n + 1),
    verify,
    runVerify,
    exporting,
    exportAs,
  };
}
