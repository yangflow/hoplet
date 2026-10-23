// Sessions that direct sessions (#67, ADR 0034): the settings of each session, the grant its token holds, the
// rules a prompt from one session to another must pass (mode, chain, limits) and when a session's tokens end.
import {
  DEFAULT_SESSION_MCP_SETTINGS,
  findProvider,
  MAX_SESSION_CHAIN,
  type RuntimeCapabilities,
  type SessionConfig,
  type SessionMcpSettings,
  type TokenCapability,
  type TokenGrant,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import type { ControlState } from './state.ts';
import type {
  Control,
  ControlEvent,
  ControlOptions,
  SessionGuard,
  SessionGuardRefusal,
  SessionLimits,
  SessionMcp,
  SessionStartGuard,
  StartRequest,
} from './types.ts';

/** What a session may do to others, unless the user, a project default or the global default says otherwise. */
export const DEFAULT_SESSION_LIMITS: SessionLimits = {
  promptsPerMinute: 6,
  promptsPerHour: 60,
  perTargetPerMinute: 3,
  unansweredPerTarget: 2,
  startsPerHour: 5,
  liveChildren: 10,
};

/** How long an agent may stay disconnected before the tokens of its sessions are revoked. */
const DEFAULT_RECONNECT_MS = 5 * 60_000;
/** Expired session tokens are removed this often. */
const SWEEP_INTERVAL_MS = 10 * 60_000;

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;

/**
 * How much a mode lets the session do without asking, lowest first. Claude Code and Codex are mapped onto the same
 * order. A mode that is not listed is not ranked, and a prompt involving it is refused.
 */
const MODE_RANK: Readonly<Record<string, number>> = {
  plan: 0,
  default: 1,
  'read-only': 1,
  acceptEdits: 2,
  'workspace-write': 2,
  auto: 3,
  'auto-review': 3,
  bypassPermissions: 4,
  'full-access': 4,
};

/** A project path no session has: the grant of a session whose project is unknown reaches no session. */
const NO_PROJECT = '\u0000';

/** The mode a session runs in, from the values its owner reported; null when none was reported. */
function modeOf(capabilities: RuntimeCapabilities | undefined): string | null {
  const option = capabilities?.config.find((o) => o.category === 'mode');
  return option?.type === 'select' ? option.currentValue : null;
}

/** What the module returns to control: the interface the API uses and the hooks control feeds. */
export interface SessionMcpModule {
  api: SessionMcp;
  onEvent(event: ControlEvent): void;
  sessionGone(sessionId: string): void;
  deviceAttached(deviceId: string): void;
  deviceDetached(deviceIds: string[]): void;
}

interface SourceUse {
  /** When each prompt to another session was reserved, oldest first. */
  all: number[];
  perTarget: Map<string, number[]>;
  /** Prompts per target the target has not yet finished a turn on. */
  unanswered: Map<string, number>;
  /** When each session start was reserved, oldest first. */
  starts: number[];
  /** Starts reserved and not yet settled, which the live children do not count yet. */
  startsInFlight: number;
}

/** States of a session whose process is up (it counts as a live child). */
const LIVE_STATES: ReadonlySet<string> = new Set([
  'starting',
  'ready',
  'running',
  'waiting-permission',
]);

export function createSessionMcp(
  state: ControlState,
  control: Pick<Control, 'ownerOf' | 'projectOf' | 'runtimeOf'>,
  options: ControlOptions,
): SessionMcpModule {
  const { store } = state;
  const limits: SessionLimits = { ...DEFAULT_SESSION_LIMITS, ...options.sessionLimits };
  const reconnectMs = options.sessionReconnectMs ?? DEFAULT_RECONNECT_MS;
  const use = new Map<string, SourceUse>();
  const timers = new Map<string, NodeJS.Timeout>();

  const sweep = setInterval(() => {
    try {
      store.sweepSessionTokens(state.now());
    } catch (err) {
      log('error', 'control', 'session token sweep failed', { error: String(err) });
    }
  }, SWEEP_INTERVAL_MS);
  sweep.unref();

  function globalDefault(): SessionMcpSettings {
    return store.sessionMcpDefault('', '') ?? DEFAULT_SESSION_MCP_SETTINGS;
  }

  function settingsOf(sessionId: string): SessionMcpSettings {
    const stored = store.sessionMcpSettings(sessionId);
    if (stored) return stored;
    const owner = control.ownerOf(sessionId);
    const project = control.projectOf(sessionId);
    const resolved =
      (owner !== null && project !== null ? store.sessionMcpDefault(owner, project) : null) ??
      globalDefault();
    // A session that is not known yet is not given settings: they would outlive nothing.
    if (owner !== null) store.seedSessionMcpSettings(sessionId, resolved, state.now());
    return resolved;
  }

  function grantOf(sessionId: string): TokenGrant | null {
    const owner = control.ownerOf(sessionId);
    if (owner === null) return null;
    const settings = settingsOf(sessionId);
    const capabilities: TokenCapability[] = ['read'];
    if (settings.send || settings.create) capabilities.push('send');
    if (settings.create) capabilities.push('create');
    if (settings.requestHandoff) capabilities.push('request-handoff');
    if (settings.visibility === 'all') return { capabilities };
    if (settings.visibility === 'machine') return { capabilities, allow: [{ deviceId: owner }] };
    return {
      capabilities,
      allow: [{ deviceId: owner, path: control.projectOf(sessionId) ?? NO_PROJECT }],
    };
  }

  function revokeTokens(sessionId: string): void {
    store.revokeSessionTokens(sessionId);
  }

  function prune(list: number[], window: number, at: number): void {
    while (list.length > 0 && at - (list[0] as number) >= window) list.shift();
  }

  function retryAfter(list: number[], window: number, at: number): number {
    const oldest = list[0] ?? at;
    return Math.max(1, Math.ceil((oldest + window - at) / 1000));
  }

  /** The refusals that do not depend on how often the session acts: itself, and a target more permissive. */
  function guardTarget(source: string, target: string): SessionGuardRefusal | null {
    if (source === target) return { ok: false, error: 'self' };

    // Never more permissive than the sender (ADR 0034).
    const from = modeOf(control.runtimeOf(source).capabilities);
    const to = modeOf(control.runtimeOf(target).capabilities);
    if (from === null || MODE_RANK[from] === undefined) {
      return { ok: false, error: 'forbidden', reason: 'sender-mode-unknown' };
    }
    if (to === null || MODE_RANK[to] === undefined) {
      return { ok: false, error: 'forbidden', reason: 'target-mode-unknown' };
    }
    if ((MODE_RANK[to] as number) > (MODE_RANK[from] as number)) {
      return { ok: false, error: 'forbidden', reason: 'target-mode-more-permissive' };
    }
    return null;
  }

  function guardPrompt(source: string, target: string): SessionGuard {
    const refusal = guardTarget(source, target);
    if (refusal) return refusal;

    const chain = [...store.sessionChain(source), source];
    if (chain.includes(target) || chain.length > MAX_SESSION_CHAIN) {
      return { ok: false, error: 'loop-limit' };
    }

    const at = state.now();
    let mine = use.get(source);
    if (!mine) {
      mine = {
        all: [],
        perTarget: new Map(),
        unanswered: new Map(),
        starts: [],
        startsInFlight: 0,
      };
      use.set(source, mine);
    }
    const targetList = mine.perTarget.get(target) ?? [];
    prune(mine.all, HOUR, at);
    prune(targetList, MINUTE, at);
    const inMinute = mine.all.filter((t) => at - t < MINUTE);
    if ((mine.unanswered.get(target) ?? 0) >= limits.unansweredPerTarget) {
      return { ok: false, error: 'rate-limited', retryAfter: 60 };
    }
    if (targetList.length >= limits.perTargetPerMinute) {
      return { ok: false, error: 'rate-limited', retryAfter: retryAfter(targetList, MINUTE, at) };
    }
    if (inMinute.length >= limits.promptsPerMinute) {
      return { ok: false, error: 'rate-limited', retryAfter: retryAfter(inMinute, MINUTE, at) };
    }
    if (mine.all.length >= limits.promptsPerHour) {
      return { ok: false, error: 'rate-limited', retryAfter: retryAfter(mine.all, HOUR, at) };
    }

    mine.all.push(at);
    targetList.push(at);
    mine.perTarget.set(target, targetList);
    mine.unanswered.set(target, (mine.unanswered.get(target) ?? 0) + 1);
    let settled = false;
    return {
      ok: true,
      settle(accepted) {
        if (settled) return;
        settled = true;
        if (accepted) return;
        // The prompt did not go out: it counts for nothing.
        const i = mine.all.lastIndexOf(at);
        if (i >= 0) mine.all.splice(i, 1);
        const j = targetList.lastIndexOf(at);
        if (j >= 0) targetList.splice(j, 1);
        const n = (mine.unanswered.get(target) ?? 1) - 1;
        if (n > 0) mine.unanswered.set(target, n);
        else mine.unanswered.delete(target);
      },
    };
  }

  /**
   * The mode a session started by `source` runs in: the one the request names when it is not more permissive than
   * the source's, else the most permissive the provider offers that is not more permissive than the source's
   * (the source's own value when offered). Null when the provider declares no mode.
   */
  function startConfig(
    source: string,
    request: Pick<StartRequest, 'deviceId' | 'provider' | 'configuration' | 'config'>,
  ): { ok: true; config: SessionConfig | undefined } | SessionGuardRefusal {
    const from = modeOf(control.runtimeOf(source).capabilities);
    if (from === null || MODE_RANK[from] === undefined) {
      return { ok: false, error: 'forbidden', reason: 'sender-mode-unknown' };
    }
    const info = findProvider(
      state.providers.get(request.deviceId) ?? [],
      request.provider,
      request.configuration,
    );
    const option = info?.capabilities.config.find((o) => o.category === 'mode');
    // A provider the core does not know is refused by the start itself.
    if (option?.type !== 'select') return { ok: true, config: request.config };
    const ceiling = MODE_RANK[from] as number;
    const named = request.config?.[option.id];
    if (named !== undefined) {
      const rank = typeof named === 'string' ? MODE_RANK[named] : undefined;
      if (rank === undefined)
        return { ok: false, error: 'forbidden', reason: 'target-mode-unknown' };
      if (rank > ceiling)
        return { ok: false, error: 'forbidden', reason: 'target-mode-more-permissive' };
      return { ok: true, config: request.config };
    }
    let best: { value: string; rank: number } | null = null;
    for (const o of option.options) {
      const rank = MODE_RANK[o.value];
      // A setting marked high risk is never chosen for a session (ADR 0033).
      if (rank === undefined || rank > ceiling || o.risk === 'high') continue;
      if (o.value === from) {
        best = { value: o.value, rank: Number.POSITIVE_INFINITY };
        break;
      }
      if (best === null || rank > best.rank) best = { value: o.value, rank };
    }
    if (best === null) return { ok: false, error: 'forbidden', reason: 'target-mode-unknown' };
    return { ok: true, config: { ...request.config, [option.id]: best.value } };
  }

  function guardStart(
    source: string,
    request: Pick<StartRequest, 'deviceId' | 'provider' | 'configuration' | 'config'>,
  ): SessionStartGuard {
    const chain = [...store.sessionChain(source), source];
    if (chain.length > MAX_SESSION_CHAIN) return { ok: false, error: 'loop-limit' };

    const resolved = startConfig(source, request);
    if (!resolved.ok) return resolved;

    const at = state.now();
    let mine = use.get(source);
    if (!mine) {
      mine = {
        all: [],
        perTarget: new Map(),
        unanswered: new Map(),
        starts: [],
        startsInFlight: 0,
      };
      use.set(source, mine);
    }
    prune(mine.starts, HOUR, at);
    if (mine.starts.length >= limits.startsPerHour) {
      return { ok: false, error: 'rate-limited', retryAfter: retryAfter(mine.starts, HOUR, at) };
    }
    const live = store
      .sessionChildren(source)
      .filter((id) => LIVE_STATES.has(control.runtimeOf(id).state)).length;
    if (live + mine.startsInFlight >= limits.liveChildren) {
      return { ok: false, error: 'rate-limited', retryAfter: 60 };
    }

    mine.starts.push(at);
    mine.startsInFlight += 1;
    const reserved = mine;
    let settled = false;
    return {
      ok: true,
      config: resolved.config ?? {},
      chain,
      settle(accepted) {
        if (settled) return;
        settled = true;
        reserved.startsInFlight -= 1;
        if (accepted) return;
        // The session was not started: it counts for nothing.
        const i = reserved.starts.lastIndexOf(at);
        if (i >= 0) reserved.starts.splice(i, 1);
      },
    };
  }

  /** A turn of `target` ended: the prompts it was given are answered. */
  function answered(target: string): void {
    for (const mine of use.values()) mine.unanswered.delete(target);
  }

  function forgetUse(sessionId: string): void {
    use.delete(sessionId);
    answered(sessionId);
  }

  function revokeOfDevice(deviceId: string): void {
    for (const sessionId of store.sweepSessionTokens(state.now())) {
      if (control.ownerOf(sessionId) === deviceId) revokeTokens(sessionId);
    }
  }

  const api: SessionMcp = {
    settingsOf,
    setSettings(sessionId, settings) {
      return store.putSessionMcpSettings(sessionId, settings, settingsOf(sessionId), state.now());
    },
    globalDefault,
    projectDefaults: () => store.sessionMcpProjectDefaults(),
    setGlobalDefault: (settings) => store.putSessionMcpDefault('', '', settings, state.now()),
    setProjectDefault: (deviceId, path, settings) =>
      store.putSessionMcpDefault(deviceId, path, settings, state.now()),
    removeProjectDefault: (deviceId, path) => store.deleteSessionMcpDefault(deviceId, path),
    grantOf,
    mintToken(sessionId) {
      const grant = grantOf(sessionId);
      // The core never hands a session the ability to answer permission requests.
      if (!grant || (grant.capabilities as string[]).includes('answer')) return null;
      const { record, secret } = store.mintSessionToken(sessionId, state.now());
      return { secret, expiresAt: record.expiresAt };
    },
    revokeTokens,
    guardPrompt,
    guardStart,
    guardInterrupt: guardTarget,
    close() {
      clearInterval(sweep);
      for (const timer of timers.values()) clearTimeout(timer);
      timers.clear();
    },
  };

  return {
    api,
    onEvent(event) {
      if (event.kind === 'turn-done') answered(event.sessionId);
      if (event.kind === 'ended' || event.kind === 'runtime-error') {
        answered(event.sessionId);
        revokeTokens(event.sessionId);
      }
    },
    sessionGone(sessionId) {
      revokeTokens(sessionId);
      forgetUse(sessionId);
      store.forgetSessionMcp(sessionId);
    },
    deviceAttached(deviceId) {
      const timer = timers.get(deviceId);
      if (timer) clearTimeout(timer);
      timers.delete(deviceId);
    },
    deviceDetached(deviceIds) {
      for (const deviceId of deviceIds) {
        const previous = timers.get(deviceId);
        if (previous) clearTimeout(previous);
        const timer = setTimeout(() => {
          timers.delete(deviceId);
          if (state.byDevice.get(deviceId)?.isOpen()) return;
          try {
            revokeOfDevice(deviceId);
          } catch (err) {
            log('error', 'control', 'session token revoke failed', { error: String(err) });
          }
        }, reconnectMs);
        timer.unref();
        timers.set(deviceId, timer);
      }
    },
  };
}
