import { type DeviceResourcesResult, RESOURCES_INTERVAL_MS } from '@hoplet/shared';
import { useEffect, useState } from 'react';
import { fetchDeviceResources } from './api.ts';
import { usePageVisible } from './visibility.ts';

/** The last hour of readings and the latest reading's sessions, each with its own last hour; null until the first answer. */
export type DeviceResources = DeviceResourcesResult | null;

/**
 * Polls one machine's resources every 15 s while the page is visible and `active` (its Machines panel is on screen).
 * Fixture mode keeps the given result.
 */
export function useDeviceResources(
  deviceId: string | undefined,
  active: boolean,
  fixture?: DeviceResources,
): DeviceResources {
  const [state, setState] = useState<DeviceResources>(fixture ?? null);
  const locked = fixture !== undefined;
  const visible = usePageVisible();

  // A new machine starts with nothing: its numbers are not the previous one's.
  // biome-ignore lint/correctness/useExhaustiveDependencies: reset exactly when the machine changes
  useEffect(() => {
    if (locked) return;
    setState(null);
  }, [locked, deviceId]);

  useEffect(() => {
    if (locked || !visible || !active || deviceId === undefined) return;
    let cancelled = false;
    const load = () => {
      void fetchDeviceResources(deviceId)
        .then((result) => {
          if (!cancelled) setState(result);
        })
        .catch(() => {
          if (!cancelled) setState(null);
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
