import * as v from 'valibot';
import { describe, expect, it } from 'vitest';
import {
  CLONE_DETAIL_MAX_BYTES,
  CloneCheckReasonSchema,
  CloneFailureReasonSchema,
  HandoffCancelResultSchema,
  HandoffRequestSchema,
  HTTP_ERROR_STATUS,
  is,
  laneOf,
  PROTOCOL_VERSION,
  WIRE_MESSAGES,
  type WireType,
} from '../src/index.ts';

const msg = (type: WireType, fields: Record<string, unknown>): boolean =>
  is(WIRE_MESSAGES[type].schema, { v: PROTOCOL_VERSION, type, ...fields });

describe('the clone wire messages (ADR 0024)', () => {
  it('is protocol version 7', () => {
    expect(PROTOCOL_VERSION).toBe(7);
  });

  it('runs every clone message on the control lane', () => {
    for (const type of Object.keys(WIRE_MESSAGES).filter((t) => t.startsWith('device.clone.'))) {
      expect(laneOf(type as WireType), type).toBe('control');
    }
  });

  it('accepts each message with its fields', () => {
    expect(msg('device.clone.check', { requestId: 'r', origin: 'git@h:o/r.git' })).toBe(true);
    expect(msg('device.clone.check', { requestId: 'r', origin: 'o', cloneInto: '/x' })).toBe(true);
    expect(
      msg('device.clone.checked', { requestId: 'r', reason: 'ok', cloneInto: '/home/me/r' }),
    ).toBe(true);
    expect(
      msg('device.clone.checked', {
        requestId: 'r',
        reason: 'destination-taken',
        detail: 'symlink',
        cloneInto: '/x',
      }),
    ).toBe(true);
    expect(
      msg('device.clone.start', {
        requestId: 'r',
        handoffId: 'h',
        origin: 'o',
        cloneInto: '/x',
      }),
    ).toBe(true);
    expect(msg('device.clone.progress', { requestId: 'r', received: 10 })).toBe(true);
    expect(msg('device.clone.progress', { requestId: 'r', received: 10, total: 100 })).toBe(true);
    expect(msg('device.clone.done', { requestId: 'r', path: '/x' })).toBe(true);
    expect(msg('device.clone.failed', { requestId: 'r', reason: 'git-failed', detail: 'x' })).toBe(
      true,
    );
    expect(msg('device.clone.failed', { requestId: 'r', reason: 'cancelled' })).toBe(true);
    expect(msg('device.clone.cancel', { requestId: 'r' })).toBe(true);
  });

  it('rejects a missing field, an unknown reason and an unknown detail', () => {
    expect(msg('device.clone.start', { requestId: 'r', origin: 'o', cloneInto: '/x' })).toBe(false);
    expect(msg('device.clone.checked', { requestId: 'r', reason: 'maybe', cloneInto: '/x' })).toBe(
      false,
    );
    expect(
      msg('device.clone.checked', {
        requestId: 'r',
        reason: 'destination-taken',
        detail: 'busy',
        cloneInto: '/x',
      }),
    ).toBe(false);
    expect(msg('device.clone.failed', { requestId: 'r', reason: 'oops' })).toBe(false);
    expect(msg('device.clone.progress', { requestId: 'r' })).toBe(false);
  });

  it('knows the clone step of a handoff', () => {
    expect(
      msg('handoff.progress', {
        sessionId: 's',
        handoffId: 'h',
        step: 'clone',
        targetDeviceId: 'd',
        sent: 1,
      }),
    ).toBe(true);
  });
});

describe('the clone reasons', () => {
  it('lists the check and failure reasons of the ADR', () => {
    expect(CloneCheckReasonSchema.options).toEqual([
      'ok',
      'no-origin',
      'no-access',
      'destination-taken',
    ]);
    expect(CloneFailureReasonSchema.options).toEqual([
      'no-access',
      'no-space',
      'destination-taken',
      'timeout',
      'git-failed',
      'cancelled',
    ]);
    expect(CLONE_DETAIL_MAX_BYTES).toBe(2048);
  });
});

describe('the REST contract', () => {
  it('has the two new error codes with their status', () => {
    expect(HTTP_ERROR_STATUS['clone-failed']).toBe(502);
    expect(HTTP_ERROR_STATUS['handoff-not-running']).toBe(409);
  });

  it('takes cloneInto next to targetPath', () => {
    const parse = (body: unknown) => v.safeParse(HandoffRequestSchema, body).success;
    expect(parse({ targetDeviceId: 'd' })).toBe(true);
    expect(parse({ targetDeviceId: 'd', cloneInto: '/x' })).toBe(true);
    expect(parse({ targetDeviceId: 'd', cloneInto: '' })).toBe(false);
    expect(parse({ targetDeviceId: 'd', cloneInto: 'x'.repeat(4097) })).toBe(false);
  });

  it('answers a cancel with ok true', () => {
    expect(is(HandoffCancelResultSchema, { ok: true })).toBe(true);
    expect(is(HandoffCancelResultSchema, { ok: false })).toBe(false);
  });
});
