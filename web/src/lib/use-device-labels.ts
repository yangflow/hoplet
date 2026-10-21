import type { DeviceInfo } from '@hoplet/shared';
import { useCallback, useEffect, useRef, useState } from 'react';
import { putDeviceLabels } from './api.ts';
import { withLabel, withLabelAt, withoutLabel } from './capabilities.ts';
import { errorText } from './errors.ts';
import { refreshDevices } from './use-devices.ts';

export type DeviceLabelsState = {
  labels: string[];
  error: string | null;
  /** The label just removed and where it was, for Undo; null once anything else changed. */
  removed: { label: string; index: number } | null;
  add: (label: string) => void;
  remove: (label: string) => void;
  undo: () => void;
};

/**
 * The labels of one machine, saved as they change: a change shows at once, the last save in flight decides what
 * stays, a failed save shows its error and returns to what the core holds. Fixture mode keeps them locally.
 */
export function useDeviceLabels(
  device: Pick<DeviceInfo, 'deviceId' | 'labels'>,
  local = false,
  /** A label already shown as removed, with Undo: the design's states. */
  removedLabel?: string,
): DeviceLabelsState {
  const { deviceId } = device;
  // What the user set and the core may not list yet; null while the device list is the truth.
  const [shown, setShown] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [removed, setRemoved] = useState<DeviceLabelsState['removed']>(
    removedLabel === undefined ? null : { label: removedLabel, index: device.labels.length },
  );
  const saving = useRef(0);
  const current = shown ?? device.labels;
  const latest = useRef(current);
  latest.current = current;

  // The list from the core moved (the save arrived, or another client changed them): follow it.
  const key = JSON.stringify(device.labels);
  // biome-ignore lint/correctness/useExhaustiveDependencies: a changed list is the trigger
  useEffect(() => {
    if (saving.current === 0) setShown(null);
  }, [key]);

  const apply = useCallback(
    (next: string[]) => {
      setShown(next);
      latest.current = next;
      setError(null);
      if (local) return;
      const mine = ++saving.current;
      void putDeviceLabels(deviceId, next)
        .then((saved) => {
          if (mine !== saving.current) return;
          saving.current = 0;
          setShown(saved.labels);
          refreshDevices();
        })
        .catch((e) => {
          if (mine !== saving.current) return;
          saving.current = 0;
          setShown(null);
          setRemoved(null);
          setError(errorText(e));
        });
    },
    [deviceId, local],
  );

  const add = useCallback(
    (label: string) => {
      setRemoved(null);
      const next = withLabel(latest.current, label);
      if (next.length !== latest.current.length) apply(next);
    },
    [apply],
  );
  const remove = useCallback(
    (label: string) => {
      const index = latest.current.indexOf(label);
      if (index < 0) return;
      apply(withoutLabel(latest.current, label));
      setRemoved({ label, index });
    },
    [apply],
  );
  const undo = useCallback(() => {
    if (!removed) return;
    setRemoved(null);
    apply(withLabelAt(latest.current, removed.label, removed.index));
  }, [apply, removed]);

  return { labels: current, error, removed, add, remove, undo };
}
