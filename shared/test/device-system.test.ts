import { describe, expect, it } from 'vitest';
import { is, PROTOCOL_VERSION, WIRE_MESSAGES } from '../src/index.ts';

const hello = (fields: Record<string, unknown>): boolean =>
  is(WIRE_MESSAGES['device.hello'].schema, {
    v: PROTOCOL_VERSION,
    type: 'device.hello',
    deviceId: 'dev-1',
    name: 'box',
    version: '1.0.0',
    ...fields,
  });

describe('the system in device.hello', () => {
  it('is optional so an agent that predates it still says hello', () => {
    expect(hello({})).toBe(true);
    expect(hello({ system: { os: 'darwin', arch: 'arm64' } })).toBe(true);
  });

  it('is an os and an arch, each a string of at most 32 characters', () => {
    for (const bad of [
      { os: 'darwin' },
      { arch: 'arm64' },
      { os: 1, arch: 'arm64' },
      { os: 'x'.repeat(33), arch: 'arm64' },
      { os: 'linux', arch: 'x'.repeat(33) },
      'darwin',
    ]) {
      expect(hello({ system: bad })).toBe(false);
    }
    expect(hello({ system: { os: 'x'.repeat(32), arch: 'y'.repeat(32) } })).toBe(true);
  });
});
