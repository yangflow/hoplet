import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError, errorText } from '../../../lib/errors.ts';
import { t } from '../../../lib/i18n.ts';

/** Where "Clear context and approve…" is: not chosen, asking, starting the new session, or failed to start it. */
export type FreshPhase = 'idle' | 'confirm' | 'starting' | 'failed';

/**
 * Why the new session did not start, as the card's note says it. The plan was not refused, so the note ends
 * by saying it still waits; a request that is gone says that instead.
 */
export function freshErrorText(err: unknown, device: string): string {
  if (err instanceof ApiError && err.code === 'request-closed') return errorText(err);
  const why = (): string => {
    if (err instanceof ApiError) {
      if (err.code === 'owner-offline' || err.code === 'target-offline') return t('it is offline');
      if (err.code === 'timeout') return t('no answer in time');
      if (err.code === 'rejected') return err.reason ? err.reason : t('it refused to start it');
      if (err.code === 'invalid-request') return t('the request was not valid');
    }
    return errorText(err);
  };
  return t(
    '{device} did not start the new session: {reason}. The plan still waits here, nothing was changed.',
  )
    .replace('{device}', device)
    .replace('{reason}', why());
}

/**
 * The state of approving a plan into a new session. `start` runs the call with a prompt ID; after a timeout the
 * next try reuses the ID, so a start that did finish is not made twice (the core deduplicates by it), and any
 * other failure begins afresh. Success leaves the phase `starting`: the app opens the new session.
 */
export function useFreshPlan(options: {
  start: (promptId: string) => Promise<void>;
  device: string;
  initial?: { phase: FreshPhase; error?: string } | undefined;
}) {
  const [phase, setPhase] = useState<FreshPhase>(options.initial?.phase ?? 'idle');
  const [error, setError] = useState<string | null>(options.initial?.error ?? null);
  const promptId = useRef<string | null>(null);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const { start: run, device } = options;

  const start = useCallback(async () => {
    const id = promptId.current ?? crypto.randomUUID();
    promptId.current = id;
    setPhase('starting');
    setError(null);
    try {
      await run(id);
    } catch (err) {
      if (!(err instanceof ApiError && err.code === 'timeout')) promptId.current = null;
      if (!mounted.current) return;
      setError(freshErrorText(err, device));
      setPhase('failed');
    }
  }, [run, device]);

  const ask = useCallback(() => setPhase('confirm'), []);
  /** Leaves the confirmation or the failure for the plain card; the next start is a new one. */
  const back = useCallback(() => {
    promptId.current = null;
    setPhase('idle');
    setError(null);
  }, []);
  return { phase, error, ask, back, start };
}
