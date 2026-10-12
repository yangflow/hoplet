import type { DeviceInfo } from '@hoplet/shared';
import { useCallback, useEffect, useState } from 'react';
import { fetchDeviceProjects } from '../../lib/api.ts';
import { onLive } from '../../lib/live.ts';

/**
 * The watched project paths of every connected device, from its catalog, so a project with no session yet
 * still has its row in the sidebar; a done handoff reloads its target's, which now watches the worktree.
 * `remember` takes a listing a mutation answered with; `enabled` is off for fixtures.
 */
export function useWatchedProjects(devices: DeviceInfo[], enabled: boolean) {
  const [watched, setWatched] = useState<Record<string, string[]>>({});
  const online = devices
    .filter((d) => d.connected)
    .map((d) => d.deviceId)
    .sort()
    .join(',');

  useEffect(() => {
    if (!enabled || online === '') return;
    let cancelled = false;
    for (const deviceId of online.split(',')) {
      fetchDeviceProjects(deviceId)
        .then((catalog) => {
          if (cancelled || !catalog) return;
          setWatched((prev) => ({ ...prev, [deviceId]: catalog.projects.map((p) => p.path) }));
        })
        .catch(() => undefined);
    }
    return () => {
      cancelled = true;
    };
  }, [enabled, online]);

  useEffect(() => {
    if (!enabled) return;
    return onLive((event) => {
      if (event.kind !== 'message' || event.msg.type !== 'handoff.progress') return;
      const { step, targetDeviceId } = event.msg;
      if (step !== 'done') return;
      fetchDeviceProjects(targetDeviceId)
        .then((catalog) => {
          if (!catalog) return;
          setWatched((prev) => ({
            ...prev,
            [targetDeviceId]: catalog.projects.map((p) => p.path),
          }));
        })
        .catch(() => undefined);
    });
  }, [enabled]);

  const remember = useCallback((deviceId: string, paths: string[]) => {
    setWatched((prev) => ({ ...prev, [deviceId]: paths }));
  }, []);
  return { watched, remember };
}
