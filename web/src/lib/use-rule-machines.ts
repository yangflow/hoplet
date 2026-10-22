import type { PermissionRuleMachinesResponse } from '@hoplet/shared';
import { useCallback, useEffect, useState } from 'react';
import { fetchRuleMachines, pushRulesAgain } from './api.ts';

/** How often the machines are asked again while the panel is on screen. */
export const RULE_MACHINES_POLL_MS = 10_000;

/**
 * Which machines have applied the latest permission rules. Asked again every few seconds; `push` sends the rules to one
 * machine again and takes the answer. A `fixture` is the answer itself and nothing is fetched.
 */
export function useRuleMachines(fixture?: PermissionRuleMachinesResponse) {
  const [data, setData] = useState<PermissionRuleMachinesResponse | null>(fixture ?? null);
  const [pushing, setPushing] = useState<string | null>(null);
  const locked = fixture !== undefined;

  useEffect(() => {
    if (locked) return;
    const abort = new AbortController();
    const load = () => {
      fetchRuleMachines(abort.signal)
        .then(setData)
        .catch(() => {
          // The panel keeps the last answer; without one it stays hidden.
        });
    };
    load();
    const tick = setInterval(load, RULE_MACHINES_POLL_MS);
    return () => {
      abort.abort();
      clearInterval(tick);
    };
  }, [locked]);

  const push = useCallback(
    async (deviceId: string) => {
      if (locked) return;
      setPushing(deviceId);
      try {
        setData(await pushRulesAgain(deviceId));
      } catch {
        // The machine went offline or is gone; the next poll shows where it stands.
      } finally {
        setPushing(null);
      }
    },
    [locked],
  );

  return { data, pushing, push };
}
