import type { GlobalSearchHit } from '@hoplet/shared';
import { useEffect, useState } from 'react';
import { fetchSearch } from '../../lib/api.ts';

const DEBOUNCE_MS = 200;

export type SearchState = {
  hits: GlobalSearchHit[];
  /** The request is in flight. */
  scanning: boolean;
};

/** Debounced global search for the palette; fixture mode keeps the given result. */
export function useSearchHits(query: string, fixture?: SearchState): SearchState {
  const [state, setState] = useState<SearchState>(fixture ?? { hits: [], scanning: false });
  const locked = fixture !== undefined;

  useEffect(() => {
    if (locked) return;
    const q = query.trim();
    if (!q) {
      setState({ hits: [], scanning: false });
      return;
    }
    let cancelled = false;
    setState((prev) => ({ hits: prev.hits, scanning: true }));
    const timer = setTimeout(() => {
      void fetchSearch(q)
        .then((r) => {
          if (!cancelled) setState({ hits: r.results, scanning: false });
        })
        .catch(() => {
          if (!cancelled) setState({ hits: [], scanning: false });
        });
    }, DEBOUNCE_MS);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [query, locked]);

  return locked && fixture ? fixture : state;
}
