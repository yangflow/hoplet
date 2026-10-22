import type { DeviceInfo } from '@hoplet/shared';
import { useEffect, useState } from 'react';
import { fetchDeviceProjects } from './api.ts';

/** The projects of every machine, as the choices of a project scope: the fixture's, else what the agents list. */
export function useProjectChoices(
  devices: readonly DeviceInfo[],
  fixture?: Record<string, string[]>,
): { deviceId: string; path: string }[] {
  const [fetched, setFetched] = useState<Record<string, string[]>>({});
  const key = devices.map((d) => d.deviceId).join('\u0000');
  // biome-ignore lint/correctness/useExhaustiveDependencies: `key` stands for the list of machines
  useEffect(() => {
    if (fixture) return;
    let live = true;
    for (const d of devices) {
      fetchDeviceProjects(d.deviceId)
        .then((r) => {
          if (live)
            setFetched((f) => ({ ...f, [d.deviceId]: r ? r.projects.map((p) => p.path) : [] }));
        })
        .catch(() => undefined);
    }
    return () => {
      live = false;
    };
  }, [key, fixture]);
  const source = fixture ?? fetched;
  return devices.flatMap((d) =>
    (source[d.deviceId] ?? []).map((path) => ({ deviceId: d.deviceId, path })),
  );
}
