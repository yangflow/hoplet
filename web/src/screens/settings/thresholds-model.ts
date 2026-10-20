import type {
  DeviceResourceSample,
  ResourceThresholdPair,
  ResourceThresholds,
} from '@hoplet/shared';

export type ThresholdKind = keyof ResourceThresholdPair;

/** The whole percents a field accepts: memory used above (1 to 100), disk free under (0 to 99). */
export const THRESHOLD_RANGE: Record<ThresholdKind, { min: number; max: number }> = {
  memory: { min: 1, max: 100 },
  disk: { min: 0, max: 99 },
};

/** What a machine warns at: its own values, else the default. */
export function effectiveThresholds(
  thresholds: ResourceThresholds,
  deviceId: string,
): ResourceThresholdPair {
  return thresholds.machines[deviceId] ?? thresholds.default;
}

/** Whether a machine has values of its own. */
export function hasOwnThresholds(thresholds: ResourceThresholds, deviceId: string): boolean {
  return thresholds.machines[deviceId] !== undefined;
}

/** The thresholds with the default replaced. */
export function withDefault(
  thresholds: ResourceThresholds,
  pair: ResourceThresholdPair,
): ResourceThresholds {
  return { ...thresholds, default: pair };
}

/** The thresholds with one machine's values set (`pair`) or removed (`null`, so it follows the default). */
export function withMachine(
  thresholds: ResourceThresholds,
  deviceId: string,
  pair: ResourceThresholdPair | null,
): ResourceThresholds {
  const { [deviceId]: _removed, ...rest } = thresholds.machines;
  return { ...thresholds, machines: pair === null ? rest : { ...rest, [deviceId]: pair } };
}

/** The whole percent a field's text means, or null when it is not a whole number in range. */
export function parseThreshold(kind: ThresholdKind, text: string): number | null {
  const trimmed = text.trim();
  if (!/^\d{1,3}$/.test(trimmed)) return null;
  const n = Number(trimmed);
  const { min, max } = THRESHOLD_RANGE[kind];
  return n >= min && n <= max ? n : null;
}

/** Which thresholds a reading crosses: memory above `memory` % used, disk under `disk` % free. */
export function readingWarn(
  s: DeviceResourceSample,
  pair: ResourceThresholdPair,
): { memory: boolean; disk: boolean } {
  return {
    memory: s.memTotal > 0 && s.memUsed * 100 > pair.memory * s.memTotal,
    disk: s.diskTotal > 0 && s.diskFree * 100 < pair.disk * s.diskTotal,
  };
}
