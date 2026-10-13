import type { DeviceDirsResult } from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';

/** The root of a browse, as the agent names it. */
export const ROOT_PATH = '~';

export type Opened = { ok: true; listing: DeviceDirsResult } | { ok: false; error: unknown };

/**
 * One directory listing at a time from a machine, fetched per directory and cached while the dialog is open
 * (`scope` names the machine: a new one starts empty). Opening a directory aborts the request it supersedes.
 */
export function useDirBrowser(
  scope: string | null,
  load: (path: string, signal: AbortSignal) => Promise<DeviceDirsResult>,
  initial?: DeviceDirsResult,
) {
  const [listing, setListing] = useState<DeviceDirsResult | null>(initial ?? null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const cache = useRef(new Map<string, DeviceDirsResult>());
  const pending = useRef<AbortController | null>(null);
  const seed = useRef(initial);
  const loader = useRef(load);
  loader.current = load;

  const open = useCallback(async (path: string): Promise<Opened> => {
    pending.current?.abort();
    const hit = cache.current.get(path);
    if (hit) {
      pending.current = null;
      setLoading(false);
      setError(null);
      setListing(hit);
      return { ok: true, listing: hit };
    }
    const controller = new AbortController();
    pending.current = controller;
    setLoading(true);
    setError(null);
    try {
      const result = await loader.current(path, controller.signal);
      if (controller.signal.aborted)
        return { ok: false, error: new DOMException('', 'AbortError') };
      cache.current.set(path, result);
      cache.current.set(result.path, result);
      setListing(result);
      setLoading(false);
      return { ok: true, listing: result };
    } catch (err) {
      if (!controller.signal.aborted) {
        setError(err);
        setLoading(false);
      }
      return { ok: false, error: err };
    }
  }, []);

  useEffect(() => {
    cache.current = new Map();
    if (seed.current) {
      cache.current.set(seed.current.path, seed.current);
      return;
    }
    if (scope === null) return;
    setListing(null);
    void open(ROOT_PATH);
    return () => pending.current?.abort();
  }, [scope, open]);

  return { listing, loading, error, open, clearError: () => setError(null) };
}
