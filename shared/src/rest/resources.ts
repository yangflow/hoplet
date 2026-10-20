import * as v from 'valibot';
import { RESOURCES_DISK_WARN_DEFAULT, RESOURCES_MEMORY_WARN_DEFAULT } from '../wire/index.ts';

/** The most machines with their own thresholds. */
export const MAX_RESOURCE_THRESHOLD_MACHINES = 200;

/**
 * When one machine warns, in whole percents: `memory` is the percent of the memory used above which it warns
 * (1 to 100), `disk` the percent of the volume free under which it warns (0 to 99).
 */
export const ResourceThresholdPairSchema = v.object({
  memory: v.pipe(v.number(), v.integer(), v.minValue(1), v.maxValue(100)),
  disk: v.pipe(v.number(), v.integer(), v.minValue(0), v.maxValue(99)),
});
export type ResourceThresholdPair = v.InferOutput<typeof ResourceThresholdPairSchema>;

/** The thresholds every machine starts with. */
export const DEFAULT_RESOURCE_THRESHOLDS: ResourceThresholds = {
  default: { memory: RESOURCES_MEMORY_WARN_DEFAULT, disk: RESOURCES_DISK_WARN_DEFAULT },
  machines: {},
};

/** GET and PUT /api/resources/thresholds: the default for every machine and the overrides, keyed by device ID. */
export const ResourceThresholdsSchema = v.object({
  default: ResourceThresholdPairSchema,
  machines: v.record(
    v.pipe(v.string(), v.minLength(1), v.maxLength(256)),
    ResourceThresholdPairSchema,
  ),
});
export type ResourceThresholds = v.InferOutput<typeof ResourceThresholdsSchema>;
