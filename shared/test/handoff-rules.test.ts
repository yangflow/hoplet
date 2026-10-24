import * as v from 'valibot';
import { describe, expect, it } from 'vitest';
import {
  HandoffRequestSchema,
  HandoffRulesPreviewSchema,
  HandoffRulesQuerySchema,
  HandoffStepSchema,
  is,
  PROTOCOL_VERSION,
  WIRE_MESSAGES,
} from '../src/index.ts';

describe('copying project rules on a handoff (ADR 0037)', () => {
  it('takes copyProjectRules as a boolean on the handoff request', () => {
    const parse = (body: unknown) => v.safeParse(HandoffRequestSchema, body).success;
    expect(parse({ targetDeviceId: 'd', copyProjectRules: true })).toBe(true);
    expect(parse({ targetDeviceId: 'd', copyProjectRules: 'yes' })).toBe(false);
  });

  it('has a rules step between verify and commit, with the count copied', () => {
    const steps = HandoffStepSchema.options;
    expect(steps.indexOf('rules')).toBe(steps.indexOf('verify') + 1);
    expect(steps.indexOf('commit')).toBe(steps.indexOf('rules') + 1);
    expect(
      is(WIRE_MESSAGES['handoff.progress'].schema, {
        v: PROTOCOL_VERSION,
        type: 'handoff.progress',
        sessionId: 's',
        handoffId: 'h',
        step: 'rules',
        targetDeviceId: 't',
        copied: 3,
      }),
    ).toBe(true);
  });

  it('asks the preview for a target and a path, and answers each rule with its state', () => {
    expect(is(HandoffRulesQuerySchema, { targetDeviceId: 't', targetPath: '/r' })).toBe(true);
    expect(is(HandoffRulesQuerySchema, { targetDeviceId: 't', targetPath: '' })).toBe(false);
    expect(is(HandoffRulesQuerySchema, { targetDeviceId: 't', targetPath: '/r', x: 1 })).toBe(
      false,
    );
    const rule = { action: 'deny', tool: 'write', match: { paths: ['/r/.env'] }, note: '' };
    const preview = {
      sourceDeviceId: 'o',
      sourcePath: null,
      targetDeviceId: 't',
      targetPath: '/r',
      rules: [{ state: 'copy', ...rule }],
    };
    expect(is(HandoffRulesPreviewSchema, preview)).toBe(true);
    expect(
      is(HandoffRulesPreviewSchema, { ...preview, rules: [{ state: 'moved', ...rule }] }),
    ).toBe(false);
  });
});
