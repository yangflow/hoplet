import type {
  DeviceCloneOffer,
  DeviceInfo,
  HandoffProgress,
  HandoffResult,
  HandoffRule,
  HandoffRulesPreview,
  HttpErrorCode,
  PermissionAction,
  Provider,
  ProviderInfo,
} from '@hoplet/shared';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { providerName } from '../../components/provider-marks.tsx';
import {
  fetchDeviceClone,
  fetchHandoffRules,
  postDeviceProviders,
  postHandoff,
  postHandoffCancel,
} from '../../lib/api.ts';
import { type HandoffChoice, handoffChoice } from '../../lib/configurations.ts';
import { ApiError, configurationErrorText, errorText } from '../../lib/errors.ts';
import { t } from '../../lib/i18n.ts';
import { onLive } from '../../lib/live.ts';
import { ruleName } from '../../lib/permission-rules.ts';
import type { CloneIntoFixture } from './clone-into.tsx';

/** Every step a run can list: the clone first (ADR 0024), the rules between verify and commit (ADR 0037). */
export const ALL_STEPS = [
  'clone',
  'prepare',
  'transfer',
  'install',
  'verify',
  'rules',
  'commit',
] as const;
export type Step = (typeof ALL_STEPS)[number];

/** The steps a run lists: the clone step only when the run clones, the rules step only when it copies rules. */
export function stepsOf(run: Pick<HandoffRun, 'clone' | 'rules'>): readonly Step[] {
  return ALL_STEPS.filter(
    (step) => (step !== 'clone' || run.clone) && (step !== 'rules' || run.rules),
  );
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
  /** Present when the run copies project rules: how many the preview offered and how many the core copied. */
  rules?: { count?: number; copied?: number };
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
  rules?: number,
): HandoffRun {
  const first: Step = clone ? 'clone' : 'prepare';
  return {
    targetDeviceId,
    ownerName,
    outcome: 'running',
    steps: { [first]: { at: now } },
    current: first,
    ...(clone ? { clone } : {}),
    ...(rules ? { rules: { count: rules } } : {}),
  };
}

function isStep(step: string): step is Step {
  return (ALL_STEPS as readonly string[]).includes(step);
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
  if (step === 'rules') {
    base = {
      ...base,
      rules: { ...base.rules, ...(msg.copied !== undefined ? { copied: msg.copied } : {}) },
    };
  }
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
    // A run that asked to copy rules learns here how many were: none when the target had them all by then.
    ...(run.rules || result.rulesCopied !== undefined
      ? { rules: { ...run.rules, copied: result.rulesCopied ?? 0 } }
      : {}),
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
  if (run.failedStep === 'rules' && code === 'target-offline')
    why = named(
      '{target} disconnected before it confirmed the rules. The copied rules were removed again.',
    );
  else if (reason === 'rules-refused')
    why = named('{target} refused the copied rules. They were removed again.');
  else if (reason === 'rules-limit')
    why = named('{target} would have more permission rules than Hoplet keeps.');
  else if (run.failedStep === 'rules' && code === 'timeout')
    why = named('{target} did not confirm the rules in time. The copied rules were removed again.');
  else if (code === 'no-clone' || reason === 'no-clone')
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

/** Whether the target's clone has the rules of the session's project, as the core compared them (ADR 0037). */
export type RulesCheck =
  /** No clone to compare with yet, or nothing to look up. */
  | { state: 'idle' }
  | { state: 'checking' }
  | { state: 'ready'; preview: HandoffRulesPreview }
  | { state: 'unknown' };

/** The compared rules counted: the copies by action, those the target has, and the target's own. */
export type RulesSummary = {
  copies: HandoffRule[];
  byAction: Record<PermissionAction, number>;
  same: number;
  own: number;
};

export function rulesSummary(rules: readonly HandoffRule[]): RulesSummary {
  const copies = rules.filter((r) => r.state === 'copy');
  const byAction: Record<PermissionAction, number> = { deny: 0, ask: 0, allow: 0 };
  for (const r of copies) byAction[r.action] += 1;
  return {
    copies,
    byAction,
    same: rules.filter((r) => r.state === 'same').length,
    own: rules.filter((r) => r.state === 'target').length,
  };
}

const fillN = (text: string, n: number, target: string) =>
  t(text).replaceAll('{n}', String(n)).replaceAll('{target}', target);

/** The line under the checkbox: the copies by action, then what the target has and keeps. */
export function rulesCountsText(summary: RulesSummary, target: string): string {
  const { byAction, same, own } = summary;
  const actions = [
    byAction.deny ? fillN('{n} deny', byAction.deny, target) : '',
    byAction.ask ? fillN('{n} ask', byAction.ask, target) : '',
    byAction.allow ? fillN('{n} allow', byAction.allow, target) : '',
  ].filter(Boolean);
  const parts = [`${actions.join(' · ')}.`];
  if (same === 0 && own === 0) parts.push(fillN('{target} has none for this project.', 0, target));
  if (same > 0) parts.push(fillN('{target} has {n} of them.', same, target));
  if (own === 1) parts.push(fillN('Its own rule stays.', 1, target));
  else if (own > 1) parts.push(fillN('Its {n} own rules stay.', own, target));
  return parts.join(' ');
}

/** The label of the checkbox. */
export function rulesCopyLabel(n: number, target: string): string {
  return n === 1
    ? fillN('Copy 1 project rule to {target}', 1, target)
    : fillN('Copy {n} project rules to {target}', n, target);
}

/** What a rule matches, written out, without its note. */
export function ruleMatchText(rule: Pick<HandoffRule, 'tool' | 'match'>): string {
  return ruleName({ ...rule, note: '' });
}

/**
 * What the session loses on the target when the box is cleared: the strongest rule it will not have and how many
 * more of its action, with `{rule}` where that rule goes. Null when nothing would be copied.
 */
export function rulesLossText(
  summary: RulesSummary,
  target: string,
): { text: string; rule: string } | null {
  const pick = (action: PermissionAction) => summary.copies.find((r) => r.action === action);
  const first = pick('deny') ?? pick('ask');
  if (!first) {
    if (summary.byAction.allow === 0) return null;
    return {
      text: fillN(
        'On {target} the session runs without this project’s {n} allow rules and asks instead.',
        summary.byAction.allow,
        target,
      ),
      rule: '',
    };
  }
  const more = summary.byAction[first.action] - 1;
  const [one, many] =
    first.action === 'deny'
      ? [
          'On {target} the session will not be stopped by deny {rule}.',
          'On {target} the session will not be stopped by deny {rule} and {n} more deny rules.',
        ]
      : [
          'On {target} the session will not ask before {rule}.',
          'On {target} the session will not ask before {rule} and {n} more ask rules.',
        ];
  return { text: fillN(more > 0 ? many : one, more, target), rule: ruleMatchText(first) };
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
  /** The compared project rules, whether the box is checked and whether the list is shown. */
  rules?: RulesCheck;
  copyRules?: boolean;
  rulesOpen?: boolean;
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
  const [rules, setRules] = useState<RulesCheck>(fixture?.rules ?? { state: 'idle' });
  const [copyRules, setCopyRules] = useState(fixture?.copyRules ?? true);
  const [rulesOpen, setRulesOpen] = useState(fixture?.rulesOpen ?? false);
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

  // The clone the rules would be copied to: the one found, the one the offer makes, or the path typed.
  const rulesPath =
    clone.state === 'found'
      ? clone.path
      : clone.state === 'offer'
        ? clone.offer.reason === 'ok'
          ? clone.offer.cloneInto
          : ''
        : clone.state === 'checking'
          ? ''
          : targetPath.trim();
  // The project rules the target's clone lacks, compared again when the target or its clone changes.
  useEffect(() => {
    if (locked) return;
    if (!open || running || !sessionId || !target || !rulesPath) {
      setRules({ state: 'idle' });
      return;
    }
    const abort = new AbortController();
    setRules({ state: 'checking' });
    // A typed path is compared once the typing pauses.
    const timer = setTimeout(() => {
      fetchHandoffRules(sessionId, target, rulesPath, abort.signal)
        .then((preview) => setRules({ state: 'ready', preview }))
        .catch(() => {
          if (!abort.signal.aborted) setRules({ state: 'unknown' });
        });
    }, 250);
    return () => {
      clearTimeout(timer);
      abort.abort();
    };
  }, [locked, open, running, sessionId, target, rulesPath]);
  // The box starts checked for every target, and the list closed.
  // biome-ignore lint/correctness/useExhaustiveDependencies: both are reset when the session or target changes
  useEffect(() => {
    if (locked) return;
    setCopyRules(true);
    setRulesOpen(false);
  }, [sessionId, target, locked]);
  const rulesToCopy =
    rules.state === 'ready' ? rules.preview.rules.filter((r) => r.state === 'copy').length : 0;

  /** Starts the handoff; `withoutRules` is the failed card's "Hand off without the rules". */
  const start = useCallback(
    (opts?: { withoutRules?: boolean }) => {
      if (!sessionId || !target || running || configurationBlocked) return;
      // A run that failed starts again where it was; otherwise the offer the panel shows decides.
      const into =
        run?.clone?.into ??
        (clone.state === 'offer' && clone.offer.reason === 'ok'
          ? clone.offer.cloneInto
          : undefined);
      const from =
        run?.clone?.origin ?? (clone.state === 'offer' ? originLabel(clone.offer.origin) : '');
      const path =
        clone.state === 'found' || into !== undefined ? undefined : targetPath.trim() || undefined;
      // A run that failed copies again what it was copying, unless the owner now hands off without the rules.
      const copying = opts?.withoutRules
        ? false
        : run
          ? run.rules !== undefined
          : copyRules && rulesToCopy > 0;
      if (opts?.withoutRules) setCopyRules(false);
      const count = copying ? (run?.rules?.count ?? rulesToCopy) : undefined;
      setRun(
        startRun(target, ownerName, Date.now(), into ? { into, origin: from } : undefined, count),
      );
      setCancelling(false);
      if (locked) return;
      // The default on the target is the vendor's built-in, so only another configuration is named.
      const named = configuration && configuration !== provider ? configuration : undefined;
      void postHandoff(sessionId, target, {
        targetPath: path,
        cloneInto: into,
        configuration: named,
        copyProjectRules: copying,
      })
        .then((result) => setRun((r) => applyResult(r, result, Date.now())))
        .catch((err: unknown) =>
          setRun((r) =>
            applyResult(r, err instanceof Error ? err : new Error(String(err)), Date.now()),
          ),
        );
    },
    [
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
      copyRules,
      rulesToCopy,
    ],
  );

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
    rules,
    copyRules,
    setCopyRules,
    rulesOpen,
    setRulesOpen,
    dismiss,
    deviceName,
  };
}

export type Handoff = ReturnType<typeof useHandoff>;
