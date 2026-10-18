import type { DeviceInfo } from '@hoplet/shared';
import { useEffect, useState } from 'react';
import { fetchDevices } from '../../lib/api.ts';
import { usePageVisible } from '../../lib/visibility.ts';

const POLL_MS = 2000;

/** Polls the device list; fixture mode keeps the given devices. */
export function useDevices(fixture?: DeviceInfo[]) {
  const [devices, setDevices] = useState<DeviceInfo[]>(fixture ?? []);
  const locked = fixture !== undefined;
  const visible = usePageVisible();

  useEffect(() => {
    if (locked || !visible) return;
    let cancelled = false;
    const load = () => {
      void fetchDevices()
        .then((r) => {
          if (cancelled) return;
          setDevices((prev) =>
            prev.length === r.devices.length &&
            prev.every(
              (d, i) =>
                d.deviceId === r.devices[i]?.deviceId &&
                d.name === r.devices[i]?.name &&
                d.connected === r.devices[i]?.connected &&
                d.lastSeen === r.devices[i]?.lastSeen &&
                d.terminal === r.devices[i]?.terminal &&
                d.previewHost === r.devices[i]?.previewHost &&
                JSON.stringify(d.providers) === JSON.stringify(r.devices[i]?.providers),
            )
              ? prev
              : r.devices,
          );
        })
        .catch(() => {
          if (!cancelled) setDevices([]);
        });
    };
    load();
    const tick = setInterval(load, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(tick);
    };
  }, [locked, visible]);

  return devices;
}
