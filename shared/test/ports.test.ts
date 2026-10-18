import * as v from 'valibot';
import { describe, expect, it } from 'vitest';
import {
  ClientMessageSchema,
  is,
  laneOf,
  PORT_PROCESS_MAX,
  PORTS_WATCH_MAX_SESSIONS,
  PROTOCOL_VERSION,
  SESSION_PORTS_MAX,
  WIRE_MESSAGES,
  type WireType,
} from '../src/index.ts';

const msg = (type: WireType, fields: Record<string, unknown>): boolean =>
  is(WIRE_MESSAGES[type].schema, { v: PROTOCOL_VERSION, type, ...fields });

const port = { port: 5173, address: 'loopback', process: 'node' };

describe('the dev server port wire messages (ADR 0026)', () => {
  it('runs both messages on the control lane', () => {
    expect(laneOf('ports.watch')).toBe('control');
    expect(laneOf('session.ports')).toBe('control');
  });

  it('accepts a watch of at most 64 valid session ids, and an empty one', () => {
    expect(msg('ports.watch', { sessionIds: [] })).toBe(true);
    expect(msg('ports.watch', { sessionIds: ['abc-1', 'def_2'] })).toBe(true);
    expect(msg('ports.watch', { sessionIds: ['../x'] })).toBe(false);
    const ids = (n: number) => Array.from({ length: n }, (_, i) => `s${i}`);
    expect(msg('ports.watch', { sessionIds: ids(PORTS_WATCH_MAX_SESSIONS) })).toBe(true);
    expect(msg('ports.watch', { sessionIds: ids(PORTS_WATCH_MAX_SESSIONS + 1) })).toBe(false);
  });

  it('accepts a report of at most 16 ports of port, address and process only', () => {
    expect(msg('session.ports', { sessionId: 's1', ports: [] })).toBe(true);
    expect(msg('session.ports', { sessionId: 's1', ports: [port] })).toBe(true);
    const many = (n: number) => Array.from({ length: n }, (_, i) => ({ ...port, port: 3000 + i }));
    expect(msg('session.ports', { sessionId: 's1', ports: many(SESSION_PORTS_MAX) })).toBe(true);
    expect(msg('session.ports', { sessionId: 's1', ports: many(SESSION_PORTS_MAX + 1) })).toBe(
      false,
    );
  });

  it('rejects an out-of-range port, an unknown address class and a long name', () => {
    for (const bad of [
      { ...port, port: 0 },
      { ...port, port: 65536 },
      { ...port, port: 1.5 },
      { ...port, address: 'lan' },
      { ...port, process: 'x'.repeat(PORT_PROCESS_MAX + 1) },
    ]) {
      expect(msg('session.ports', { sessionId: 's1', ports: [bad] })).toBe(false);
    }
    expect(
      msg('session.ports', {
        sessionId: 's1',
        ports: [{ ...port, process: 'x'.repeat(PORT_PROCESS_MAX) }],
      }),
    ).toBe(true);
  });

  it('lets the agent send session.ports and announce a preview host', () => {
    expect(
      v.is(ClientMessageSchema, {
        v: PROTOCOL_VERSION,
        type: 'session.ports',
        sessionId: 's1',
        ports: [port],
      }),
    ).toBe(true);
    expect(
      msg('device.hello', {
        deviceId: 'dev-1',
        name: 'box',
        version: '1.0.0',
        providers: [],
        previewHost: 'box.ts.net',
      }),
    ).toBe(true);
  });
});
