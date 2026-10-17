import type { HandoffProgress } from '@hoplet/shared';
import { PROTOCOL_VERSION } from '@hoplet/shared';
import { describe, expect, it } from 'vitest';
import { ApiError } from '../src/lib/errors.ts';
import {
  applyProgress,
  applyResult,
  failureCode,
  failureDetail,
  failureText,
  type HandoffRun,
  originLabel,
  startRun,
  stepsOf,
} from '../src/screens/workspace/handoff.ts';
import { formatProgress, formatSeconds } from '../src/screens/workspace/handoff-panel.tsx';

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

/** Folds messages into a run, one second apart. */
function fold(run: HandoffRun | null, ...msgs: HandoffProgress[]): HandoffRun | null {
  return msgs.reduce<HandoffRun | null>(
    (r, msg, i) => applyProgress(r, msg, 1000 * (i + 1), 'MacBook Pro'),
    run,
  );
}

describe('a handoff run', () => {
  it('follows the steps of a handoff this window started and times each one', () => {
    const run = fold(
      startRun('dev-studio', 'MacBook Pro', 0),
      progress('prepare'),
      progress('transfer', { sent: 0, total: 100 }),
      progress('transfer', { sent: 60, total: 100 }),
      progress('install'),
    );
    expect(run).toMatchObject({
      handoffId: 'h1',
      outcome: 'running',
      current: 'install',
      sent: 60,
      total: 100,
      steps: {
        prepare: { at: 0, end: 2000 },
        transfer: { at: 2000, end: 4000 },
        install: { at: 4000 },
      },
    });
  });

  it('ends done with the worktree, and every step closed', () => {
    const run = fold(
      startRun('dev-studio', 'MacBook Pro', 0),
      progress('prepare'),
      progress('commit'),
      progress('done', { worktree: '/w/hoplet-s1', branch: 'hoplet/s1' }),
    );
    expect(run).toMatchObject({
      outcome: 'done',
      current: null,
      worktree: '/w/hoplet-s1',
      branch: 'hoplet/s1',
      steps: { commit: { at: 2000, end: 3000 } },
    });
  });

  it('marks the step under way as the one that failed', () => {
    const run = fold(
      startRun('dev-studio', 'MacBook Pro', 0),
      progress('prepare'),
      progress('verify'),
      progress('failed', { reason: 'verify-failed' }),
    );
    expect(run).toMatchObject({ outcome: 'failed', failedStep: 'verify', reason: 'verify-failed' });
  });

  it('follows a handoff another window started, but does not show only the end of one', () => {
    expect(fold(null, progress('done'))).toBeNull();
    expect(fold(null, progress('failed'))).toBeNull();
    const run = fold(null, progress('transfer', { sent: 1, total: 2 }));
    expect(run).toMatchObject({
      handoffId: 'h1',
      targetDeviceId: 'dev-studio',
      ownerName: 'MacBook Pro',
      current: 'transfer',
    });
  });

  it('starts over for another handoff and keeps the snapshot time of an offline one', () => {
    const first = fold(null, progress('prepare'), progress('failed'));
    const second = fold(first, progress('prepare', { handoffId: 'h2', snapshotAt: 42 }));
    expect(second).toMatchObject({ handoffId: 'h2', outcome: 'running', snapshotAt: 42 });
  });

  it('ends with the answer to the request when no progress ended it', () => {
    const started = startRun('dev-studio', 'MacBook Pro', 0);
    const done = applyResult(
      started,
      { ok: true, ownerDeviceId: 'dev-studio', ownerEpoch: 1, worktree: '/w', branch: 'hoplet/x' },
      500,
    );
    expect(done).toMatchObject({
      outcome: 'done',
      worktree: '/w',
      steps: { prepare: { at: 0, end: 500 } },
    });
    const refused = applyResult(started, new ApiError(409, 'busy'), 500);
    expect(refused).toMatchObject({
      outcome: 'failed',
      failedStep: 'prepare',
      error: { code: 'busy' },
    });
    // The failure reported after the refused request does not reopen the run.
    expect(fold(refused, progress('failed', { reason: 'busy' }))).toBe(refused);
  });

  it('names why it failed and that nothing moved', () => {
    const run: HandoffRun = {
      ...startRun('dev-studio', 'MacBook Pro', 0),
      outcome: 'failed',
      current: null,
      error: { code: 'rejected', reason: 'base-commit-unavailable' },
    };
    expect(failureCode(run)).toBe('base-commit-unavailable');
    expect(failureText(run, 'studio')).toBe(
      'studio does not have the base commit. Fetch in its clone and try again. Nothing moved; MacBook Pro still owns the session.',
    );
    expect(failureText({ ...run, error: { code: 'verify-failed' } }, 'studio')).toMatch(
      /^studio's copy did not match/,
    );
    expect(failureCode({ ...run, error: undefined, reason: 'timeout' } as HandoffRun)).toBe(
      'timeout',
    );
  });
});

describe('a handoff that clones first', () => {
  const clone = { into: '/Users/mark/github/hoplet', origin: 'github.com/yangflow/hoplet' };
  const started = () => startRun('dev-studio', 'MacBook Pro', 0, clone);

  it('lists six steps with the clone first, and five without', () => {
    expect(stepsOf(started())).toEqual([
      'clone',
      'prepare',
      'transfer',
      'install',
      'verify',
      'commit',
    ]);
    expect(stepsOf(startRun('dev-studio', 'MacBook Pro', 0))).toHaveLength(5);
  });

  it('shows the bytes received and keeps them when the transfer counts its own', () => {
    const cloning = fold(
      started(),
      progress('clone', { sent: 20, total: 50 }),
      progress('clone', { sent: 30 }),
    );
    expect(cloning).toMatchObject({ current: 'clone', sent: 30 });
    const moving = fold(cloning, progress('prepare'), progress('transfer', { sent: 1, total: 9 }));
    expect(moving).toMatchObject({
      current: 'transfer',
      cloned: 30,
      sent: 1,
      total: 9,
      steps: { clone: { at: 0, end: 1000 } },
    });
  });

  it('fails the clone step with the reason the core gave and says what was left', () => {
    const run = fold(started(), progress('clone'), progress('failed', { reason: 'no-space' }));
    expect(run).toMatchObject({ outcome: 'failed', failedStep: 'clone', reason: 'no-space' });
    expect(failureCode(run as HandoffRun)).toBe('no-space');
    expect(failureText(run as HandoffRun, 'studio')).toBe(
      'The clone stopped: no space left on studio. Nothing was left at /Users/mark/github/hoplet. MacBook Pro still owns the session.',
    );
  });

  it('gives each failure reason its own words, and git only for git-failed', () => {
    const failed = (reason: string, detail?: string): HandoffRun => ({
      ...started(),
      outcome: 'failed',
      failedStep: 'clone',
      current: null,
      error: { code: 'clone-failed', reason, ...(detail ? { detail } : {}) },
    });
    expect(failureText(failed('no-access'), 'studio')).toMatch(
      /^studio cannot read github.com\/yangflow\/hoplet: authentication failed\. Hoplet never asks for a password\./,
    );
    expect(failureText(failed('destination-taken'), 'studio')).toMatch(/Choose another folder/);
    expect(failureText(failed('timeout'), 'studio')).toMatch(/took too long/);
    expect(failureText(failed('cancelled'), 'studio')).toMatch(/was cancelled/);
    expect(failureText(failed('git-failed', 'fatal: nope'), 'studio')).not.toMatch(/nope/);
    expect(failureDetail(failed('git-failed', 'fatal: nope'))).toBe('fatal: nope');
    expect(failureDetail(failed('no-space', 'fatal: nope'))).toBeUndefined();
  });

  it('does not claim a path it does not know', () => {
    const other: HandoffRun = {
      ...startRun('dev-studio', 'MacBook Pro', 0, {}),
      outcome: 'failed',
      failedStep: 'clone',
      current: null,
      error: { code: 'clone-failed', reason: 'timeout' },
    };
    expect(failureText(other, 'studio')).not.toMatch(/Nothing was left/);
  });
});

describe('handoff formats', () => {
  it('writes remotes as people read them', () => {
    expect(originLabel('git@github.com:o/r.git')).toBe('github.com/o/r');
    expect(originLabel('https://github.com/o/r/')).toBe('github.com/o/r');
    expect(originLabel('ssh://git@host.test:22/o/r.git')).toBe('host.test/o/r');
    expect(originLabel('/srv/git/r.git')).toBe('/srv/git/r');
  });

  it('writes step times and transfer progress as the design does', () => {
    expect(formatSeconds(800)).toBe('0.8 s');
    expect(formatSeconds(12_400)).toBe('12 s');
    expect(formatProgress(6_501_171, 9_905_190)).toBe('6.2 / 9.4 MiB');
    expect(formatProgress(512_000, 9_905_190)).toBe('500 KiB / 9.4 MiB');
  });
});
