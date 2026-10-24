import type { HandoffProgress, HandoffResult } from '@hoplet/shared';
import { PROTOCOL_VERSION } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import { ApiError } from '../src/lib/errors.ts';
import { handoffRules } from '../src/screens/workspace/fixtures.ts';
import {
  applyProgress,
  applyResult,
  failureText,
  type HandoffRun,
  rulesCopyLabel,
  rulesCountsText,
  rulesLossText,
  rulesSummary,
  startRun,
  stepsOf,
} from '../src/screens/workspace/handoff.ts';

const progress = (step: HandoffProgress['step'], over: Partial<HandoffProgress> = {}) =>
  ({
    v: PROTOCOL_VERSION,
    type: 'handoff.progress',
    sessionId: 's1',
    handoffId: 'h1',
    step,
    targetDeviceId: 'dev-studio',
    ...over,
  }) as HandoffProgress;

const result = (over: Partial<HandoffResult> = {}) =>
  ({ worktree: '/w', branch: 'b', ...over }) as HandoffResult;

describe('handoff rules step', () => {
  it('lists the rules step only when the run copies rules', () => {
    expect(stepsOf({})).toEqual(['prepare', 'transfer', 'install', 'verify', 'commit']);
    expect(stepsOf({ rules: { count: 3 } })).toEqual([
      'prepare',
      'transfer',
      'install',
      'verify',
      'rules',
      'commit',
    ]);
    expect(stepsOf({ clone: {}, rules: {} })).toHaveLength(7);
  });

  it('starts a run with the rules it offers, and none when there are none', () => {
    expect(startRun('dev-studio', 'MacBook Pro', 0, undefined, 3).rules).toEqual({ count: 3 });
    expect(startRun('dev-studio', 'MacBook Pro', 0, undefined, 0).rules).toBeUndefined();
  });

  it('folds the rules step and the count it copied', () => {
    let run: HandoffRun | null = startRun('dev-studio', 'MacBook Pro', 0, undefined, 3);
    run = applyProgress(run, progress('verify'), 1000, 'MacBook Pro');
    run = applyProgress(run, progress('rules', { copied: 3 }), 2000, 'MacBook Pro');
    expect(run?.current).toBe('rules');
    expect(run?.rules).toEqual({ count: 3, copied: 3 });
    expect(run?.steps.verify?.end).toBe(2000);
  });

  it('learns a rules step from another window', () => {
    const run = applyProgress(null, progress('rules'), 0, 'MacBook Pro');
    expect(run && stepsOf(run)).toContain('rules');
  });

  it('takes the copied count from the result', () => {
    const run = startRun('dev-studio', 'MacBook Pro', 0, undefined, 3);
    expect(applyResult(run, result({ rulesCopied: 2 }), 1)?.rules).toEqual({ count: 3, copied: 2 });
    expect(applyResult(run, result(), 1)?.rules).toEqual({ count: 3, copied: 0 });
    const plain = startRun('dev-studio', 'MacBook Pro', 0);
    expect(applyResult(plain, result(), 1)?.rules).toBeUndefined();
  });
});

describe('handoff rules failures', () => {
  const failed = (code: string | null, reason?: string): HandoffRun => ({
    ...startRun('dev-studio', 'MacBook Pro', 0, undefined, 3),
    outcome: 'failed',
    current: null,
    failedStep: 'rules',
    error: { code: code as never, ...(reason ? { reason } : {}) },
  });

  it('says the copied rules were removed again', () => {
    expect(failureText(failed('target-offline'), 'studio')).toMatch(
      /^studio disconnected before it confirmed the rules\. The copied rules were removed again\. Nothing moved/,
    );
    expect(failureText(failed('timeout'), 'studio')).toMatch(/did not confirm the rules in time/);
    expect(failureText(failed('handoff-failed', 'rules-refused'), 'studio')).toMatch(
      /^studio refused the copied rules/,
    );
    expect(failureText(failed('handoff-failed', 'rules-limit'), 'studio')).toMatch(
      /more permission rules than Hoplet keeps/,
    );
  });

  it('keeps the other reasons for the other steps', () => {
    const run = { ...failed('target-offline'), failedStep: 'transfer' as const };
    expect(failureText(run, 'studio')).toMatch(/^studio went offline\./);
    expect(
      failureText(
        applyResult(
          startRun('dev-studio', 'MacBook Pro', 0),
          new ApiError(409, 'busy'),
          1,
        ) as HandoffRun,
        'studio',
      ),
    ).toMatch(/A turn is running/);
  });
});

describe('handoff rules preview', () => {
  it('counts the copies by action, the shared and the target’s own', () => {
    const summary = rulesSummary(handoffRules('differ').rules);
    expect(summary.copies).toHaveLength(3);
    expect(summary.byAction).toEqual({ deny: 2, ask: 0, allow: 1 });
    expect(summary.same).toBe(1);
    expect(summary.own).toBe(1);
    expect(rulesCountsText(summary, 'studio')).toBe(
      '2 deny · 1 allow. studio has 1 of them. Its own rule stays.',
    );
    expect(rulesCountsText(rulesSummary(handoffRules('none').rules), 'studio')).toBe(
      '2 deny · 1 ask · 1 allow. studio has none for this project.',
    );
  });

  it('labels the checkbox by count', () => {
    expect(rulesCopyLabel(1, 'studio')).toBe('Copy 1 project rule to studio');
    expect(rulesCopyLabel(3, 'studio')).toBe('Copy 3 project rules to studio');
  });

  it('names the strongest rule the session would lose', () => {
    const loss = rulesLossText(rulesSummary(handoffRules('differ').rules), 'studio');
    expect(loss?.text).toBe(
      'On studio the session will not be stopped by deny {rule} and 1 more deny rules.',
    );
    expect(loss?.rule).toMatch(/terraform apply/);
    expect(rulesLossText(rulesSummary(handoffRules('same').rules), 'studio')).toBeNull();
  });

  it('falls back to ask, then to the allow count', () => {
    const rules = handoffRules('none').rules;
    const ask = rulesLossText(rulesSummary(rules.filter((r) => r.action !== 'deny')), 'studio');
    expect(ask?.text).toBe('On studio the session will not ask before {rule}.');
    expect(ask?.rule).toMatch(/pnpm publish/);
    const allow = rulesLossText(rulesSummary(rules.filter((r) => r.action === 'allow')), 'studio');
    expect(allow).toEqual({
      text: 'On studio the session runs without this project’s 1 allow rules and asks instead.',
      rule: '',
    });
  });
});
