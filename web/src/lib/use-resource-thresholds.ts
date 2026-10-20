import type { ResourceThresholds } from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchResourceThresholds, putResourceThresholds } from './api.ts';
import { errorText } from './errors.ts';
import { refreshDevices } from './use-devices.ts';

/** How long an edit rests before it is saved: typing a number is one save, not one per key. */
export const THRESHOLDS_SAVE_DELAY_MS = 400;

export type ResourceThresholdsState = {
  /** null until the core answered. */
  thresholds: ResourceThresholds | null;
  error: string | null;
  /** Applies a change at once and saves it shortly after the last one. */
  change: (next: ResourceThresholds) => void;
};

/**
 * The warning thresholds, saved as the user edits them: the whole document is replaced by the last edit, the last
 * save in flight decides what stays, a failed save shows its error and returns to what the core holds, and a saved
 * change reloads the device list so every warning follows at once. Fixture mode keeps the given thresholds locally.
 */
export function useResourceThresholds(fixture?: ResourceThresholds): ResourceThresholdsState {
  const [thresholds, setThresholds] = useState<ResourceThresholds | null>(fixture ?? null);
  const [error, setError] = useState<string | null>(null);
  const locked = fixture !== undefined;
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const pending = useRef<ResourceThresholds | null>(null);
  const saving = useRef(0);

  useEffect(() => {
    if (locked) return;
    let cancelled = false;
    void fetchResourceThresholds()
      .then((r) => {
        if (!cancelled) setThresholds(r);
      })
      .catch((e) => {
        if (!cancelled) setError(errorText(e));
      });
    return () => {
      cancelled = true;
    };
  }, [locked]);

  const save = useCallback((next: ResourceThresholds) => {
    const mine = ++saving.current;
    void putResourceThresholds(next)
      .then((saved) => {
        if (mine !== saving.current) return;
        if (pending.current === null) setThresholds(saved);
        refreshDevices();
      })
      .catch((e) => {
        if (mine !== saving.current) return;
        setError(errorText(e));
        void fetchResourceThresholds()
          .then((r) => {
            if (mine === saving.current && pending.current === null) setThresholds(r);
          })
          .catch(() => undefined);
      });
  }, []);

  const flush = useCallback(() => {
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = null;
    const next = pending.current;
    pending.current = null;
    if (next) save(next);
  }, [save]);

  // An edit still resting when the page closes is saved, not lost.
  useEffect(() => flush, [flush]);

  const change = useCallback(
    (next: ResourceThresholds) => {
      setThresholds(next);
      if (locked) return;
      setError(null);
      pending.current = next;
      if (timer.current !== null) clearTimeout(timer.current);
      timer.current = setTimeout(flush, THRESHOLDS_SAVE_DELAY_MS);
    },
    [locked, flush],
  );

  return { thresholds, error, change };
}
