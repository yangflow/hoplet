import type {
  DeviceCloneOffer,
  DeviceInfo,
  HandoffProgress,
  HandoffResult,
  HttpErrorCode,
  Provider,
  ProviderInfo,
} from '@hoplet/shared';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { providerName } from '../../components/provider-marks.tsx';
import {
  fetchDeviceClone,
  postDeviceProviders,
  postHandoff,
  postHandoffCancel,
} from '../../lib/api.ts';
import { type HandoffChoice, handoffChoice } from '../../lib/configurations.ts';
import { ApiError, configurationErrorText, errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { onLive } from '../../lib/live.ts';
import type { CloneIntoFixture } from './clone-into.tsx';

/** The steps of a handoff as the panel lists them (docs/spec.md#handoff). */
export const STEPS = ['prepare', 'transfer', 'install', 'verify', 'commit'] as const;
/** The steps of a handoff that clones the repository on the target first (ADR 0024). */
export const CLONE_STEPS = ['clone', ...STEPS] as const;
export type Step = (typeof CLONE_STEPS)[number];

/** The steps a run lists: the clone step comes first only when the run clones. */
export function stepsOf(run: Pick<HandoffRun, 'clone'>): readonly Step[] {
  return run.clone ? CLONE_STEPS : STEPS;
}

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
  /** Bytes the clone step received, kept when the step ends and `sent` counts the transfer. */
  cloned?: number;
  /** The time of the snapshot an offline handoff installs. */
  snapshotAt?: number;
  worktree?: string;
  branch?: string;
  /** Present when the run clones first: the folder and the origin as people read it, when this window knows them. */
  clone?: { into?: string; origin?: string };
  /** The step that failed, when one was under way. */
  failedStep?: Step;
  /** The core's reason in `handoff.progress`, or the code and the agent's reason of the refused request. */
  reason?: string;
  error?: { code: HttpErrorCode | null; reason?: string; detail?: string };
};

/** Whether the target has a clone of the session's repository among its watched projects. */
export type CloneCheck =
  | { state: 'checking' }
  | { state: 'found'; path: string }
  /** No clone, and the core's offer to make one: a destination the agent accepts or the reason it does not. */
  | { state: 'offer'; offer: DeviceCloneOffer }
  | { state: 'none' }
  | { state: 'unknown'; text: string };

/** A run this window starts. */
export function startRun(
  targetDeviceId: string,
  ownerName: string,
  now: number,
  clone?: { into: string; origin: string },
): HandoffRun {
  const first: Step = clone ? 'clone' : 'prepare';
  return {
    targetDeviceId,
    ownerName,
    outcome: 'running',
    steps: { [first]: { at: now } },
    current: first,
    ...(clone ? { clone } : {}),
  };
}

function isStep(step: string): step is Step {
  return (CLONE_STEPS as readonly string[]).includes(step);
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
  if (step === 'clone' && !base.clone) base = { ...base, clone: {} };
  const steps = step === base.current ? base.steps : closeCurrent(base, now);
  if (base.current === 'clone' && step !== 'clone') {
    const { sent, total: _total, ...rest } = base;
    base = { ...rest, ...(sent !== undefined ? { cloned: sent } : {}) };
  }
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
          ? {
              code: result.code,
              ...(result.reason ? { reason: result.reason } : {}),
              ...(result.detail ? { detail: result.detail } : {}),
            }
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
  const named = (text: string) =>
    t(text).replaceAll('{target}', target).replaceAll('{owner}', owner);
  const code = run.error?.code ?? run.reason;
  const reason = run.error?.reason ?? run.reason;
  if (run.failedStep === 'clone' || code === 'clone-failed')
    return `${cloneFailureText(run, target, reason)} ${named('{owner} still owns the session.')}`;
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

/** What a failed clone says, by the reason the agent gave; git's own text is `failureDetail`. */
function cloneFailureText(run: HandoffRun, target: string, reason: string | undefined): string {
  const into = run.clone?.into;
  const named = (text: string) =>
    t(text)
      .replaceAll('{target}', target)
      .replaceAll('{origin}', run.clone?.origin ?? t('the repository'))
      .replaceAll('{host}', run.clone?.origin?.split('/')[0] ?? t('the remote'))
      .replaceAll('{path}', into ?? '');
  const left = into ? ` ${named('Nothing was left at {path}.')}` : '';
  switch (reason) {
    case 'no-access':
      return named(CLONE_NO_ACCESS);
    case 'no-space':
      return `${named('The clone stopped: no space left on {target}.')}${left}`;
    case 'destination-taken':
      return `${named('The clone stopped: the folder is taken on {target}. Choose another folder.')}${left}`;
    case 'timeout':
      return `${named('The clone took too long and was stopped.')}${left}`;
    case 'cancelled':
      return `${named('The clone was cancelled.')}${left}`;
    default:
      return `${named('git could not finish the clone on {target}. Try again.')}${left}`;
  }
}

/** The fixed advice when a target cannot read the repository: never git's own text, never a password prompt.
 * The check could not read the repository: the network or the credentials, it cannot tell which. */
export const CLONE_UNREADABLE =
  '{target} could not reach or read {origin}: the network or the credentials failed. Hoplet never asks for a password. Check the connection, give git on {target} an SSH key or a credential helper for {host}, or clone the repository there yourself and hand off again.';

/** A clone the remote refused: only the credentials can be at fault. */
export const CLONE_NO_ACCESS =
  '{target} cannot read {origin}: authentication failed. Hoplet never asks for a password. Give git on {target} an SSH key or a credential helper for {host}, or clone the repository there yourself and hand off again.';

/** What git said when it failed (credentials removed by the agent), for the `git-failed` reason only. */
export function failureDetail(run: HandoffRun): string | undefined {
  const reason = run.error?.reason ?? run.reason;
  return reason === 'git-failed' ? run.error?.detail : undefined;
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
export type HandoffFixture = {
  run?: HandoffRun;
  clone?: CloneCheck;
  targetPath?: string;
  /** The "Clone into" dialog open over the card, with what it shows. */
  dialog?: CloneIntoFixture;
  /** The configuration picked on the target, and whether its list is open. */
  configuration?: string;
  configurationOpen?: boolean;
  /** The ID of the target's configuration an Enable is waiting on. */
  enabling?: string;
};

/**
 * The handoff of the selected session: the connected machines it can go to, whether the chosen one has a clone
 * of its repository (looked up while the panel is open), the path to use when it has none, and the run.
 */
export function useHandoff(opts: {
  sessionId: string | null;
  ownerDeviceId: string | undefined;
  ownerName: string;
  /** The session's vendor and the ID of its configuration (absent for the vendor's built-in). */
  provider?: Provider | undefined;
  configuration?: string | undefined;
  configurationName?: string | undefined;
  devices: readonly DeviceInfo[];
  /** The URL of the repository's `origin` from the listing or the snapshot; undefined until one was read. */
  origin: string | undefined;
  open: boolean;
  fixture?: HandoffFixture | undefined;
}) {
  const { sessionId, ownerDeviceId, ownerName, devices, origin, open, fixture } = opts;
  const provider = opts.provider;
  const sessionConfiguration = opts.configuration ?? provider;
  const locked = fixture !== undefined;
  const [targetId, setTargetId] = useState('');
  const [targetPath, setTargetPath] = useState(fixture?.targetPath ?? '');
  const [run, setRun] = useState<HandoffRun | null>(fixture?.run ?? null);
  const [clone, setClone] = useState<CloneCheck>(fixture?.clone ?? { state: 'checking' });
  const [attempt, setAttempt] = useState(0);
  const [picked, setPicked] = useState(fixture?.configuration ?? '');
  const [cancelling, setCancelling] = useState(false);
  const [enabling, setEnabling] = useState<string | undefined>(fixture?.enabling);
  const [enableError, setEnableError] = useState<string | undefined>();
  // The answer of an Enable, shown until the devices report the same change.
  const [enabled, setEnabled] = useState<
    Record<string, { base: string; providers: ProviderInfo[] }>
  >({});

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

  // A pick belongs to one session and one target.
  // biome-ignore lint/correctness/useExhaustiveDependencies: the pick is forgotten when either changes
  useEffect(() => {
    if (!locked) setPicked('');
  }, [sessionId, target, locked]);

  // The configuration the session continues in on the target.
  const reported = otherDevices.find((d) => d.deviceId === target)?.providers;
  const answer = enabled[target];
  const targetProviders =
    answer && answer.base === JSON.stringify(reported) ? answer.providers : reported;
  const choice: HandoffChoice = useMemo(
    () =>
      provider && sessionConfiguration
        ? handoffChoice(targetProviders ?? [], provider, sessionConfiguration)
        : { state: 'builtin' },
    [targetProviders, provider, sessionConfiguration],
  );
  const options = choice.state === 'same' || choice.state === 'pick' ? choice.options : [];
  const configuration = options.some((p) => p.configuration.id === picked)
    ? picked
    : choice.state === 'same'
      ? choice.value
      : '';
  /** The target cannot continue the session until a configuration is chosen (or exists). */
  const configurationBlocked =
    choice.state === 'none' ||
    choice.state === 'disabled' ||
    (choice.state === 'pick' && !configuration);

  // An Enable belongs to one target; an error to the card that showed it.
  // biome-ignore lint/correctness/useExhaustiveDependencies: both are forgotten when the session or target changes
  useEffect(() => {
    if (locked) return;
    setEnableError(undefined);
  }, [sessionId, target, locked]);

  /** Enables one of the target's configurations; the select then preselects it. */
  const enable = useCallback(
    (id: string) => {
      const device = otherDevices.find((d) => d.deviceId === target);
      if (!device || locked || enabling !== undefined) return;
      const base = JSON.stringify(device.providers);
      setEnabling(id);
      setEnableError(undefined);
      postDeviceProviders(device.deviceId, { id, enabled: true })
        .then((result) => {
          setEnabled((cur) => ({
            ...cur,
            [device.deviceId]: { base, providers: result.providers },
          }));
          setPicked(id);
        })
        .catch((err: unknown) =>
          setEnableError(configurationErrorText(err, device.name || device.deviceId, id)),
        )
        .finally(() => setEnabling(undefined));
    },
    [otherDevices, target, locked, enabling],
  );

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
  // biome-ignore lint/correctness/useExhaustiveDependencies: `attempt` runs the lookup again on "Try again".
  useEffect(() => {
    if (locked || !open || running || !target || origin === undefined) return;
    if (!origin) {
      setClone({ state: 'none' });
      return;
    }
    const abort = new AbortController();
    setClone({ state: 'checking' });
    fetchDeviceClone(target, origin, abort.signal)
      .then((r) =>
        setClone(
          r.path
            ? { state: 'found', path: r.path }
            : r.clone && r.clone.reason !== 'no-origin'
              ? { state: 'offer', offer: r.clone }
              : { state: 'none' },
        ),
      )
      .catch((err: unknown) => {
        if (!abort.signal.aborted) setClone({ state: 'unknown', text: errorText(err) });
      });
    return () => abort.abort();
  }, [locked, open, running, target, origin, attempt]);

  const start = useCallback(() => {
    if (!sessionId || !target || running || configurationBlocked) return;
    // A run that failed starts again where it was; otherwise the offer the panel shows decides.
    const into =
      run?.clone?.into ??
      (clone.state === 'offer' && clone.offer.reason === 'ok' ? clone.offer.cloneInto : undefined);
    const from =
      run?.clone?.origin ?? (clone.state === 'offer' ? originLabel(clone.offer.origin) : '');
    const path =
      clone.state === 'found' || into !== undefined ? undefined : targetPath.trim() || undefined;
    setRun(startRun(target, ownerName, Date.now(), into ? { into, origin: from } : undefined));
    setCancelling(false);
    if (locked) return;
    // The default on the target is the vendor's built-in, so only another configuration is named.
    const named = configuration && configuration !== provider ? configuration : undefined;
    void postHandoff(sessionId, target, {
      targetPath: path,
      cloneInto: into,
      configuration: named,
    })
      .then((result) => setRun((r) => applyResult(r, result, Date.now())))
      .catch((err: unknown) =>
        setRun((r) =>
          applyResult(r, err instanceof Error ? err : new Error(String(err)), Date.now()),
        ),
      );
  }, [
    sessionId,
    target,
    running,
    run,
    clone,
    targetPath,
    ownerName,
    locked,
    configuration,
    configurationBlocked,
    provider,
  ]);

  /** Looks the clone up again, as after the user gave the machine access. */
  const recheck = useCallback(() => setAttempt((n) => n + 1), []);

  /**
   * Asks the core whether the target can clone into `path`. An accepted destination becomes the offer; the answer
   * returns either way so the folder dialog can say why not.
   */
  const chooseDestination = useCallback(
    async (path: string): Promise<DeviceCloneOffer | null> => {
      if (!target || !origin) return null;
      const r = await fetchDeviceClone(target, origin, undefined, path);
      if (r.path) {
        setClone({ state: 'found', path: r.path });
        return null;
      }
      if (r.clone?.reason === 'ok') setClone({ state: 'offer', offer: r.clone });
      return r.clone ?? null;
    },
    [target, origin],
  );

  /** Stops the clone step of the run under way; the run then ends as failed with the reason `cancelled`. */
  const cancel = useCallback(() => {
    if (!sessionId || locked) return;
    setCancelling(true);
    void postHandoffCancel(sessionId).catch(() => undefined);
  }, [sessionId, locked]);

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
    cancel,
    cancelling,
    recheck,
    chooseDestination,
    dialogFixture: fixture?.dialog,
    configurationChoice: choice,
    configuration,
    setConfiguration: setPicked,
    configurationBlocked,
    configurationOpen: fixture?.configurationOpen ?? false,
    vendorName: provider ? providerName(provider) : '',
    configurationName: opts.configurationName ?? sessionConfiguration ?? '',
    sessionBuiltIn: sessionConfiguration === provider,
    enable,
    enabling,
    enableError,
    dismiss,
    deviceName,
  };
}

export type Handoff = ReturnType<typeof useHandoff>;
