import { type DeviceResourcesResult, RESOURCES_INTERVAL_MS } from '@hoplet/shared';
import { useEffect, useRef, useState } from 'react';
import { appendHistory, type SessionHistory } from '../screens/settings/resources-model.ts';
import { fetchDeviceResources } from './api.ts';
import { usePageVisible } from './visibility.ts';

export type DeviceResources = {
  /** The last hour of readings and the latest reading's sessions; null until the first answer. */
  result: DeviceResourcesResult | null;
  /** Each session's memory over the polls so far, for its trend line. */
  history: SessionHistory;
};

const NONE: DeviceResources = { result: null, history: {} };

/**
 * Polls one machine's resources every 15 s while the page is visible and `active` (its Machines panel is on screen).
 * Fixture mode keeps the given result. The per-session trend builds up from the polls: the API reports the latest
 * reading's sessions only.
 */
export function useDeviceResources(
  deviceId: string | undefined,
  active: boolean,
  fixture?: DeviceResources,
): DeviceResources {
  const [state, setState] = useState<DeviceResources>(fixture ?? NONE);
  const locked = fixture !== undefined;
  const visible = usePageVisible();
  const last = useRef<{ deviceId: string; at: number | undefined } | null>(null);

  // A new machine starts with nothing: its numbers are not the previous one's.
  // biome-ignore lint/correctness/useExhaustiveDependencies: reset exactly when the machine changes
  useEffect(() => {
    if (locked) return;
    setState(NONE);
    last.current = null;
  }, [locked, deviceId]);

  useEffect(() => {
    if (locked || !visible || !active || deviceId === undefined) return;
    let cancelled = false;
    const load = () => {
      void fetchDeviceResources(deviceId)
        .then((result) => {
          if (cancelled) return;
          const fresh = last.current?.deviceId !== deviceId || last.current.at !== result.at;
          last.current = { deviceId, at: result.at };
          setState((prev) => ({
            result,
            history: appendHistory(prev.history, result.sessions, fresh),
          }));
        })
        .catch(() => {
          if (!cancelled) setState(NONE);
        });
    };
    load();
    const tick = setInterval(load, RESOURCES_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(tick);
    };
  }, [locked, visible, active, deviceId]);

  return state;
}
