import { describe, expect, it } from 'vitest';
import {
  ClientMessageSchema,
  DEVICE_LABEL_MAX,
  DeviceInfoSchema,
  DeviceLabelSchema,
  DeviceLabelsRequestSchema,
  INVENTORY_GPUS_MAX,
  INVENTORY_REPOSITORIES_MAX,
  INVENTORY_TOOLCHAINS_MAX,
  is,
  laneOf,
  MAX_DEVICE_LABELS,
  PROTOCOL_VERSION,
  WIRE_MESSAGES,
} from '../src/index.ts';

const frame = (fields: Record<string, unknown> = {}) => ({
  v: PROTOCOL_VERSION,
  type: 'device.inventory',
  at: 1,
  toolchains: [{ name: 'git', version: '2.45.1' }],
  gpus: [{ name: 'RTX 4090', memory: 25769803776 }, { name: 'Apple M3' }],
  repositories: [
    { path: '/work/a', origin: 'https://github.com/o/a.git' },
    { path: '/work/b', origin: null },
  ],
  ...fields,
});
const ok = (fields?: Record<string, unknown>) =>
  is(WIRE_MESSAGES['device.inventory'].schema, frame(fields));
const list = (n: number, make: (i: number) => unknown) =>
  Array.from({ length: n }, (_, i) => make(i));

describe('device.inventory', () => {
  it('runs on the control lane from the device and is a client message', () => {
    expect(laneOf('device.inventory')).toBe('control');
    expect(is(ClientMessageSchema, frame())).toBe(true);
  });

  it('accepts empty lists, a GPU without memory and a repository without origin', () => {
    expect(ok({ toolchains: [], gpus: [], repositories: [] })).toBe(true);
  });

  it('rejects a missing origin, a negative or fractional size and a bad time', () => {
    expect(ok({ repositories: [{ path: '/a' }] })).toBe(false);
    expect(ok({ gpus: [{ name: 'g', memory: -1 }] })).toBe(false);
    expect(ok({ gpus: [{ name: 'g', memory: 1.5 }] })).toBe(false);
    expect(ok({ at: -1 })).toBe(false);
    expect(ok({ at: 1.5 })).toBe(false);
  });

  it('limits the lists and the strings', () => {
    const tool = () => ({ name: 'a', version: '1' });
    expect(ok({ toolchains: list(INVENTORY_TOOLCHAINS_MAX, tool) })).toBe(true);
    expect(ok({ toolchains: list(INVENTORY_TOOLCHAINS_MAX + 1, tool) })).toBe(false);
    expect(ok({ gpus: list(INVENTORY_GPUS_MAX, () => ({ name: 'g' })) })).toBe(true);
    expect(ok({ gpus: list(INVENTORY_GPUS_MAX + 1, () => ({ name: 'g' })) })).toBe(false);
    const repo = () => ({ path: '/p', origin: null });
    expect(ok({ repositories: list(INVENTORY_REPOSITORIES_MAX, repo) })).toBe(true);
    expect(ok({ repositories: list(INVENTORY_REPOSITORIES_MAX + 1, repo) })).toBe(false);
    expect(ok({ toolchains: [{ name: 'x'.repeat(33), version: '1' }] })).toBe(false);
    expect(ok({ toolchains: [{ name: 'x', version: 'v'.repeat(65) }] })).toBe(false);
    expect(ok({ gpus: [{ name: 'g'.repeat(129) }] })).toBe(false);
    expect(ok({ repositories: [{ path: 'p'.repeat(4097), origin: null }] })).toBe(false);
    expect(ok({ repositories: [{ path: '/p', origin: 'o'.repeat(2049) }] })).toBe(false);
  });
});

describe('device labels', () => {
  it('are lowercase letters, digits and hyphens, 1 to 32 characters', () => {
    for (const label of ['gpu', 'a', '0', 'home-lab', 'a-', 'x'.repeat(DEVICE_LABEL_MAX)]) {
      expect(is(DeviceLabelSchema, label), label).toBe(true);
    }
    for (const label of [
      '',
      'Gpu',
      'has space',
      '-a',
      'a_b',
      'ü',
      'x'.repeat(DEVICE_LABEL_MAX + 1),
    ]) {
      expect(is(DeviceLabelSchema, label), label).toBe(false);
    }
  });

  it('are at most 20 in a request', () => {
    const labels = (n: number) => list(n, (i) => `l${i}`);
    expect(is(DeviceLabelsRequestSchema, { labels: labels(MAX_DEVICE_LABELS) })).toBe(true);
    expect(is(DeviceLabelsRequestSchema, { labels: labels(MAX_DEVICE_LABELS + 1) })).toBe(false);
    expect(is(DeviceLabelsRequestSchema, {})).toBe(false);
  });

  it('are listed with a device whether or not it has an inventory', () => {
    const base = { deviceId: 'd', name: 'n', connected: false, system: null, providers: [] };
    expect(is(DeviceInfoSchema, { ...base, labels: [] })).toBe(true);
    expect(is(DeviceInfoSchema, base)).toBe(false);
    const { v: _v, type: _t, ...inventory } = frame();
    expect(is(DeviceInfoSchema, { ...base, labels: ['gpu'], inventory })).toBe(true);
  });
});
