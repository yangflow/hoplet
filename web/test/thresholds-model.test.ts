import type { DeviceResourceSample, ResourceThresholds } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import {
  effectiveThresholds,
  hasOwnThresholds,
  parseThreshold,
  readingWarn,
  withDefault,
  withMachine,
} from '../src/screens/settings/thresholds-model.ts';

const GB = 1024 ** 3;
const sample = (over: Partial<DeviceResourceSample> = {}): DeviceResourceSample => ({
  at: 1,
  cpu: 10,
  memUsed: 20 * GB,
  memTotal: 32 * GB,
  diskFree: 184e9,
  diskTotal: 994e9,
  ...over,
});
const base: ResourceThresholds = {
  default: { memory: 90, disk: 10 },
  machines: { 'dev-studio': { memory: 97, disk: 10 } },
};

describe('effectiveThresholds', () => {
  it('uses the machine’s own values, else the default', () => {
    expect(effectiveThresholds(base, 'dev-studio')).toEqual({ memory: 97, disk: 10 });
    expect(effectiveThresholds(base, 'dev-mac')).toEqual({ memory: 90, disk: 10 });
    expect(hasOwnThresholds(base, 'dev-studio')).toBe(true);
    expect(hasOwnThresholds(base, 'dev-mac')).toBe(false);
  });
  it('follows a changed default for machines without their own', () => {
    const next = withDefault(base, { memory: 80, disk: 20 });
    expect(effectiveThresholds(next, 'dev-mac')).toEqual({ memory: 80, disk: 20 });
    expect(effectiveThresholds(next, 'dev-studio')).toEqual({ memory: 97, disk: 10 });
  });
});

describe('withMachine', () => {
  it('creates an override and removes it again, leaving the others alone', () => {
    const added = withMachine(base, 'dev-mac', { memory: 80, disk: 5 });
    expect(added.machines).toEqual({
      'dev-studio': { memory: 97, disk: 10 },
      'dev-mac': { memory: 80, disk: 5 },
    });
    const removed = withMachine(added, 'dev-studio', null);
    expect(removed.machines).toEqual({ 'dev-mac': { memory: 80, disk: 5 } });
    expect(removed.default).toEqual(base.default);
    expect(base.machines['dev-studio']).toEqual({ memory: 97, disk: 10 });
  });
});

describe('parseThreshold', () => {
  it('accepts whole percents in range', () => {
    expect(parseThreshold('memory', '90')).toBe(90);
    expect(parseThreshold('memory', ' 1 ')).toBe(1);
    expect(parseThreshold('memory', '100')).toBe(100);
    expect(parseThreshold('disk', '0')).toBe(0);
    expect(parseThreshold('disk', '99')).toBe(99);
  });
  it('rejects what is out of range or not a whole number', () => {
    expect(parseThreshold('memory', '0')).toBeNull();
    expect(parseThreshold('memory', '101')).toBeNull();
    expect(parseThreshold('disk', '100')).toBeNull();
    expect(parseThreshold('disk', '')).toBeNull();
    expect(parseThreshold('disk', '1.5')).toBeNull();
    expect(parseThreshold('disk', '-1')).toBeNull();
    expect(parseThreshold('memory', '9e1')).toBeNull();
    expect(parseThreshold('memory', 'abc')).toBeNull();
  });
});

describe('readingWarn', () => {
  const d = { memory: 90, disk: 10 };
  it('warns on memory above the threshold, not at it', () => {
    expect(readingWarn(sample({ memUsed: 29 * GB }), d).memory).toBe(true);
    expect(readingWarn(sample({ memUsed: 28.8 * GB }), d).memory).toBe(false);
  });
  it('warns on disk under the threshold, not at it', () => {
    expect(readingWarn(sample({ diskFree: 99e9, diskTotal: 1000e9 }), d).disk).toBe(true);
    expect(readingWarn(sample({ diskFree: 100e9, diskTotal: 1000e9 }), d).disk).toBe(false);
  });
  it('moves with the thresholds', () => {
    const s = sample({ memUsed: 30 * GB });
    expect(readingWarn(s, { memory: 90, disk: 10 }).memory).toBe(true);
    expect(readingWarn(s, { memory: 97, disk: 10 }).memory).toBe(false);
    const full = sample({ diskFree: 0, diskTotal: 1000e9 });
    expect(readingWarn(full, { memory: 90, disk: 0 }).disk).toBe(false);
  });
  it('does not warn without totals', () => {
    expect(readingWarn(sample({ memTotal: 0, diskTotal: 0 }), d)).toEqual({
      memory: false,
      disk: false,
    });
  });
});
