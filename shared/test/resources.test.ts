import { describe, expect, it } from 'vitest';
import {
  ClientMessageSchema,
  is,
  laneOf,
  PROTOCOL_VERSION,
  RESOURCES_SESSIONS_MAX,
  WIRE_MESSAGES,
} from '../src/index.ts';

const frame = (fields: Record<string, unknown> = {}) => ({
  v: PROTOCOL_VERSION,
  type: 'device.resources',
  at: 1,
  cpu: 10,
  memUsed: 1,
  memTotal: 2,
  diskFree: 3,
  diskTotal: 4,
  sessions: [],
  ...fields,
});
const ok = (fields?: Record<string, unknown>) =>
  is(WIRE_MESSAGES['device.resources'].schema, frame(fields));

describe('device.resources', () => {
  it('runs on the control lane from the device and is a client message', () => {
    expect(laneOf('device.resources')).toBe('control');
    expect(is(ClientMessageSchema, frame())).toBe(true);
  });

  it('accepts a first reading without CPU, and sessions with an unknown CPU', () => {
    expect(ok({ cpu: null })).toBe(true);
    expect(ok({ sessions: [{ sessionId: 's1', cpu: null, rss: 5, children: 0 }] })).toBe(true);
  });

  it('rejects a percent out of range and negative or fractional sizes', () => {
    expect(ok({ cpu: 100.5 })).toBe(false);
    expect(ok({ cpu: -1 })).toBe(false);
    expect(ok({ memUsed: -1 })).toBe(false);
    expect(ok({ diskTotal: 1.5 })).toBe(false);
    expect(ok({ sessions: [{ sessionId: '../x', cpu: 1, rss: 1, children: 0 }] })).toBe(false);
  });

  it('accepts at most 64 sessions', () => {
    const list = (n: number) =>
      Array.from({ length: n }, (_, i) => ({ sessionId: `s${i}`, cpu: 1, rss: 1, children: 0 }));
    expect(ok({ sessions: list(RESOURCES_SESSIONS_MAX) })).toBe(true);
    expect(ok({ sessions: list(RESOURCES_SESSIONS_MAX + 1) })).toBe(false);
  });
});
