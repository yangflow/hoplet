import { useEffect, useState } from 'react';
import { fetchDeviceProjects } from '../../lib/api.ts';
import { pathLeaf } from './group.ts';

/** A watched project directory; `branch` is the git branch checked out there, when the catalog says. */
export type Dir = { path: string; name: string; branch?: string };

/**
 * The watched project directories of a device, from its catalog (`GET /api/devices/:id/projects`). Until
 * they arrive, or when the device is offline, `fallback` stands in. Only these are valid starting points.
 */
export function useDeviceDirs(
  deviceId: string | null,
  fallback: Dir[],
  enabled: boolean,
  /** Read again when this changes (a project was added). */
  version = 0,
): { dirs: Dir[]; loading: boolean } {
  const [loaded, setLoaded] = useState<{ deviceId: string; dirs: Dir[] } | null>(null);
  const [loading, setLoading] = useState(false);

  // biome-ignore lint/correctness/useExhaustiveDependencies: a new version reads the catalog again
  useEffect(() => {
    if (!enabled || !deviceId) return;
    let cancelled = false;
    setLoading(true);
    fetchDeviceProjects(deviceId)
      .then((catalog) => {
        if (cancelled) return;
        setLoaded(
          catalog
            ? {
                deviceId,
                dirs: catalog.projects
                  .map((p) => ({
                    path: p.path,
                    name: pathLeaf(p.path),
                    ...(p.branch ? { branch: p.branch } : {}),
                  }))
                  .sort((a, b) => a.name.localeCompare(b.name)),
              }
            : null,
        );
      })
      .catch(() => {
        if (!cancelled) setLoaded(null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [deviceId, enabled, version]);

  return {
    dirs: loaded && loaded.deviceId === deviceId ? loaded.dirs : fallback,
    loading,
  };
}
