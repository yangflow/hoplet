import type { DeviceInfo, HandoffProgress, HandoffResult, HttpErrorCode } from '@hoplet/shared';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { fetchDeviceClone, postHandoff } from '../../lib/api.ts';
import { ApiError, errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { onLive } from '../../lib/live.ts';

/** The steps of a handoff as the panel lists them (docs/spec.md#handoff). */
export const STEPS = ['prepare', 'transfer', 'install', 'verify', 'commit'] as const;
export type Step = (typeof STEPS)[number];

/** When this window saw a step start and end; a step it joined late has no start. */
export type StepTimes = { at?: number; end?: number };

/**
 * A handoff of the selected session as this window saw it: started here, or reported by `handoff.progress`
 * from a handoff another window started. It ends `done` or `failed` and stays until the panel closes.
 */
export type HandoffRun = {
  handoffId?: string;
  targetDeviceId: string;
  /** The owner's name when the handoff started; after the commit the session names its new owner. */
  ownerName: string;
  outcome: 'running' | 'failed' | 'done';
  steps: Partial<Record<Step, StepTimes>>;
  /** The step under way. */
  current: Step | null;
  /** Bytes of the snapshot archive and the session stream sent to the target, of `total`. */
  sent?: number;
  total?: number;
  /** The time of the snapshot an offline handoff installs. */
  snapshotAt?: number;
  worktree?: string;
  branch?: string;
  /** The step that failed, when one was under way. */
  failedStep?: Step;
  /** The core's reason in `handoff.progress`, or the code and the agent's reason of the refused request. */
  reason?: string;
  error?: { code: HttpErrorCode | null; reason?: string };
};

/** Whether the target has a clone of the session's repository among its watched projects. */
export type CloneCheck =
  | { state: 'checking' }
  | { state: 'found'; path: string }
  | { state: 'none' }
  | { state: 'unknown'; text: string };

/** A run this window starts. */
export function startRun(targetDeviceId: string, ownerName: string, now: number): HandoffRun {
  return {
    targetDeviceId,
    ownerName,
    outcome: 'running',
    steps: { prepare: { at: now } },
    current: 'prepare',
  };
}

function isStep(step: string): step is Step {
  return (STEPS as readonly string[]).includes(step);
}

/** Ends the step under way at `now`. */
function closeCurrent(run: HandoffRun, now: number): HandoffRun['steps'] {
  if (!run.current) return run.steps;
  return { ...run.steps, [run.current]: { ...run.steps[run.current], end: now } };
}

/**
 * Folds one `handoff.progress` of the session into the run. A message of another handoff starts a new run,
 * except a final one: a window that saw nothing of a handoff does not show only its end.
 */
export function applyProgress(
  run: HandoffRun | null,
  msg: HandoffProgress,
  now: number,
  ownerName: string,
): HandoffRun | null {
  const final = msg.step === 'done' || msg.step === 'failed';
  const same = run !== null && (run.handoffId === undefined || run.handoffId === msg.handoffId);
  // A run this window started takes the ID of the first message; a finished one is not reopened.
  if (same && run.handoffId === undefined && run.outcome !== 'running') return run;
  let base: HandoffRun;
  if (same) base = { ...run, handoffId: msg.handoffId };
  else if (final) return run;
  else
    base = {
      handoffId: msg.handoffId,
      targetDeviceId: msg.targetDeviceId,
      ownerName,
      outcome: 'running',
      steps: {},
      current: null,
    };
  if (msg.snapshotAt !== undefined) base.snapshotAt = msg.snapshotAt;
  if (msg.step === 'done') {
    if (base.outcome !== 'running') return base;
    return {
      ...base,
      steps: closeCurrent(base, now),
      current: null,
      outcome: 'done',
      ...(msg.worktree !== undefined ? { worktree: msg.worktree } : {}),
      ...(msg.branch !== undefined ? { branch: msg.branch } : {}),
    };
  }
  if (msg.step === 'failed') {
    const failedStep = base.failedStep ?? base.current ?? undefined;
    return {
      ...base,
      outcome: 'failed',
      current: null,
      ...(failedStep ? { failedStep } : {}),
      ...(msg.reason !== undefined ? { reason: msg.reason } : {}),
    };
  }
  if (base.outcome !== 'running' || !isStep(msg.step)) return base;
  const step = msg.step;
  const steps = step === base.current ? base.steps : closeCurrent(base, now);
  const next: HandoffRun = {
    ...base,
    steps: { ...steps, [step]: steps[step] ?? { at: now } },
    current: step,
  };
  if (msg.sent !== undefined) next.sent = msg.sent;
  if (msg.total !== undefined) next.total = msg.total;
  return next;
}

/** The answer to the request this window sent: the run ends with it if no progress ended it first. */
export function applyResult(
  run: HandoffRun | null,
  result: HandoffResult | ApiError | Error,
  now: number,
): HandoffRun | null {
  if (!run) return run;
  if (result instanceof Error) {
    const failedStep = run.failedStep ?? run.current ?? undefined;
    return {
      ...run,
      outcome: 'failed',
      current: null,
      ...(failedStep ? { failedStep } : {}),
      error:
        result instanceof ApiError
          ? { code: result.code, ...(result.reason ? { reason: result.reason } : {}) }
          : { code: null, reason: result.message },
    };
  }
  if (run.outcome === 'done') return run;
  return {
    ...run,
    steps: closeCurrent(run, now),
    current: null,
    outcome: 'done',
    worktree: result.worktree,
    branch: result.branch,
    ...(result.snapshotAt !== undefined ? { snapshotAt: result.snapshotAt } : {}),
  };
}

/** The short code a failed step shows: the agent's reason, the error code, or the core's reason. */
export function failureCode(run: HandoffRun): string {
  return run.error?.reason ?? run.error?.code ?? run.reason ?? 'internal';
}

/** What a failed handoff says: why, in words, and that nothing moved. */
export function failureText(run: HandoffRun, target: string): string {
  const owner = run.ownerName;
  const named = (text: string) => t(text).replace('{target}', target).replace('{owner}', owner);
  const code = run.error?.code ?? run.reason;
  const reason = run.error?.reason ?? run.reason;
  let why: string;
  if (code === 'no-clone' || reason === 'no-clone')
    why = named('{target} has no clone of this repository there.');
  else if (reason === 'base-commit-unavailable')
    why = named('{target} does not have the base commit. Fetch in its clone and try again.');
  else if (code === 'verify-failed')
    why = named("{target}'s copy did not match the session or the working tree.");
  else if (code === 'target-offline') why = named('{target} went offline.');
  else if (code === 'owner-offline') why = named('{owner} went offline.');
  else if (code === 'timeout') why = t('A machine did not answer in time.');
  else if (code === 'busy') why = t('A turn is running. Hand off when it ends.');
  else if (run.error) why = errorText(new ApiError(0, run.error.code, run.error.reason));
  else why = errorText(new ApiError(0, null, reason));
  return `${why} ${named('Nothing moved; {owner} still owns the session.')}`;
}

/** A remote URL as people read it: `git@github.com:o/r.git` is `github.com/o/r`. */
export function originLabel(origin: string): string {
  const url = origin
    .trim()
    .replace(/\.git$/, '')
    .replace(/\/+$/, '');
  const scheme = /^[a-z+]+:\/\/(?:[^@/]*@)?([^/:]+)(?::\d+)?\/(.*)$/i.exec(url);
  if (scheme) return `${scheme[1]}/${scheme[2]}`;
  const scp = /^(?:[^@/]*@)?([^/:]+):(.*)$/.exec(url);
  if (scp) return `${scp[1]}/${scp[2]}`;
  return url;
}

/** Development fixtures: what the panel shows without asking anyone. */
export type HandoffFixture = { run?: HandoffRun; clone?: CloneCheck; targetPath?: string };

/**
 * The handoff of the selected session: the connected machines it can go to, whether the chosen one has a clone
 * of its repository (looked up while the panel is open), the path to use when it has none, and the run.
 */
export function useHandoff(opts: {
  sessionId: string | null;
  ownerDeviceId: string | undefined;
  ownerName: string;
  devices: readonly DeviceInfo[];
  /** The URL of the repository's `origin` from the listing or the snapshot; undefined until one was read. */
  origin: string | undefined;
  open: boolean;
  fixture?: HandoffFixture | undefined;
}) {
  const { sessionId, ownerDeviceId, ownerName, devices, origin, open, fixture } = opts;
  const locked = fixture !== undefined;
  const [targetId, setTargetId] = useState('');
  const [targetPath, setTargetPath] = useState(fixture?.targetPath ?? '');
  const [run, setRun] = useState<HandoffRun | null>(fixture?.run ?? null);
  const [clone, setClone] = useState<CloneCheck>(fixture?.clone ?? { state: 'checking' });

  const otherDevices = useMemo(
    () => devices.filter((d) => d.connected && d.deviceId !== ownerDeviceId),
    [devices, ownerDeviceId],
  );
  const target =
    otherDevices.find((d) => d.deviceId === targetId)?.deviceId ?? otherDevices[0]?.deviceId ?? '';

  // biome-ignore lint/correctness/useExhaustiveDependencies: another session starts without the last one's handoff
  useEffect(() => {
    if (locked) return;
    setRun(null);
    setTargetPath('');
  }, [sessionId, locked]);

  // Progress of this session's handoffs, also of one another window started.
  useEffect(() => {
    if (locked || !sessionId) return;
    return onLive((event) => {
      if (event.kind !== 'message') return;
      const msg = event.msg;
      if (msg.type !== 'handoff.progress' || msg.sessionId !== sessionId) return;
      setRun((r) => applyProgress(r, msg, Date.now(), ownerName));
    });
  }, [locked, sessionId, ownerName]);

  // The clone the target would use, looked up while the panel is open and no handoff runs.
  const running = run?.outcome === 'running';
  useEffect(() => {
    if (locked || !open || running || !target || origin === undefined) return;
    if (!origin) {
      setClone({ state: 'none' });
      return;
    }
    const abort = new AbortController();
    setClone({ state: 'checking' });
    fetchDeviceClone(target, origin, abort.signal)
      .then((r) => setClone(r.path ? { state: 'found', path: r.path } : { state: 'none' }))
      .catch((err: unknown) => {
        if (!abort.signal.aborted) setClone({ state: 'unknown', text: errorText(err) });
      });
    return () => abort.abort();
  }, [locked, open, running, target, origin]);

  const start = useCallback(() => {
    if (!sessionId || !target || running) return;
    const path = clone.state === 'found' ? undefined : targetPath.trim() || undefined;
    setRun(startRun(target, ownerName, Date.now()));
    if (locked) return;
    void postHandoff(sessionId, target, path)
      .then((result) => setRun((r) => applyResult(r, result, Date.now())))
      .catch((err: unknown) =>
        setRun((r) =>
          applyResult(r, err instanceof Error ? err : new Error(String(err)), Date.now()),
        ),
      );
  }, [sessionId, target, running, clone, targetPath, ownerName, locked]);

  /** A device's name; the target of a finished handoff owns the session and is no longer among the others. */
  const deviceName = useCallback(
    (id: string) => devices.find((d) => d.deviceId === id)?.name || id,
    [devices],
  );

  /** Forgets a finished run, as when the panel closes. */
  const dismiss = useCallback(() => setRun((r) => (r?.outcome === 'running' ? r : null)), []);

  return {
    otherDevices,
    origin,
    targetId: target,
    setTargetId,
    clone,
    targetPath,
    setTargetPath,
    run,
    start,
    dismiss,
    deviceName,
  };
}

export type Handoff = ReturnType<typeof useHandoff>;
