import * as v from 'valibot';
import { describe, expect, it } from 'vitest';
import {
  ClientMessageSchema,
  is,
  laneOf,
  PROTOCOL_VERSION,
  TERMINAL_CHUNK_BYTES,
  TERMINAL_MAX_COLS,
  TERMINAL_MAX_ROWS,
  TerminalReasonSchema,
  WIRE_MESSAGES,
  type WireType,
} from '../src/index.ts';

const msg = (type: WireType, fields: Record<string, unknown>): boolean =>
  is(WIRE_MESSAGES[type].schema, { v: PROTOCOL_VERSION, type, ...fields });

const b64 = (bytes: number): string => Buffer.alloc(bytes, 'a').toString('base64');

describe('the terminal wire messages (ADR 0026)', () => {
  it('runs every terminal message on the control lane', () => {
    for (const type of [
      'terminal.open',
      'terminal.input',
      'terminal.resize',
      'terminal.ack',
      'terminal.close',
      'terminal.opened',
      'terminal.output',
      'terminal.closed',
      'terminal.rejected',
    ] as const) {
      expect(laneOf(type)).toBe('control');
    }
  });

  it('knows the reasons of a refusal or an end', () => {
    for (const reason of [
      'disabled',
      'terminal-busy',
      'turn-running',
      'external',
      'handoff-running',
      'owner-offline',
      'not-a-directory',
      'idle',
      'max-age',
      'detached',
      'turn-started',
      'handoff',
      'deleted',
      'closed',
      'input-flood',
      'exit',
    ]) {
      expect(is(TerminalReasonSchema, reason)).toBe(true);
    }
    expect(is(TerminalReasonSchema, 'because')).toBe(false);
  });

  it('bounds the size of a terminal', () => {
    const open = (cols: number, rows: number) =>
      msg('terminal.open', { terminalId: 't1', sessionId: 's1', cols, rows });
    expect(open(80, 24)).toBe(true);
    expect(open(TERMINAL_MAX_COLS, TERMINAL_MAX_ROWS)).toBe(true);
    expect(open(0, 24)).toBe(false);
    expect(open(80, 0)).toBe(false);
    expect(open(TERMINAL_MAX_COLS + 1, 24)).toBe(false);
    expect(open(80, TERMINAL_MAX_ROWS + 1)).toBe(false);
    expect(open(80.5, 24)).toBe(false);
    expect(msg('terminal.resize', { terminalId: 't1', cols: 501, rows: 24 })).toBe(false);
  });

  it('carries input and output as base64 of at most 4 KiB', () => {
    expect(msg('terminal.input', { terminalId: 't1', data: b64(TERMINAL_CHUNK_BYTES) })).toBe(true);
    expect(msg('terminal.input', { terminalId: 't1', data: b64(TERMINAL_CHUNK_BYTES + 3) })).toBe(
      false,
    );
    expect(msg('terminal.input', { terminalId: 't1', data: 'not base64!' })).toBe(false);
    expect(msg('terminal.output', { terminalId: 't1', seq: 0, data: b64(10) })).toBe(true);
    expect(msg('terminal.output', { terminalId: 't1', seq: -1, data: b64(10) })).toBe(false);
  });

  it('acknowledges whole byte counts', () => {
    expect(msg('terminal.ack', { terminalId: 't1', bytes: 4096 })).toBe(true);
    expect(msg('terminal.ack', { terminalId: 't1', bytes: -1 })).toBe(false);
  });

  it('ends a terminal with a reason and, for an exit, the code', () => {
    expect(msg('terminal.closed', { terminalId: 't1', reason: 'exit', exitCode: 0 })).toBe(true);
    expect(msg('terminal.closed', { terminalId: 't1', reason: 'idle' })).toBe(true);
    expect(msg('terminal.closed', { terminalId: 't1', reason: 'nope' })).toBe(false);
    expect(msg('terminal.rejected', { terminalId: 't1', reason: 'turn-running' })).toBe(true);
  });

  it('accepts the agent messages from a device and the capability in the hello', () => {
    for (const body of [
      { type: 'terminal.opened', terminalId: 't1' },
      { type: 'terminal.output', terminalId: 't1', seq: 0, data: b64(3) },
      { type: 'terminal.closed', terminalId: 't1', reason: 'exit', exitCode: 1 },
      { type: 'terminal.rejected', terminalId: 't1', reason: 'disabled' },
      {
        type: 'device.hello',
        deviceId: 'd1',
        name: 'box',
        version: '0.5.0',
        capabilities: { terminal: true },
      },
    ]) {
      expect(v.is(ClientMessageSchema, { v: PROTOCOL_VERSION, ...body })).toBe(true);
    }
    expect(
      v.is(ClientMessageSchema, { v: PROTOCOL_VERSION, type: 'terminal.open', terminalId: 't1' }),
    ).toBe(false);
  });
});
