import {
  type AgentRuntimeState,
  type ConfigSetting,
  type DeviceBacklogSession,
  type PlanExit,
  PROTOCOL_VERSION,
  type RuntimeCapabilities,
  type RuntimeConfig,
  type RuntimeInterrupt,
  type RuntimePermissionAnswer,
  type RuntimePrompt,
  type RuntimeStop,
  type SessionPermission,
  type SessionRule,
  type SessionRulesUnenforced,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import { toolInputSummary } from '../store/index.ts';
import { askWaiter, settleWaiter } from './pending.ts';
import {
  armExpiry,
  closePermission,
  closeSessionPermissions,
  permissionKey,
} from './permissions.ts';
import { type ControlState, onlineLink, type Reconnect } from './state.ts';
import type {
  CommandActor,
  Control,
  ControlReply,
  DeviceLink,
  PermissionAnswer,
  RuntimeCommandOutcome,
  RuntimeView,
} from './types.ts';

/** The state of one session's process as its owner reported it. */
export interface RuntimeEntry {
  deviceId: string;
  state: AgentRuntimeState;
  error?: string;
  /** `capabilities.config` carries the session's values. */
  capabilities: RuntimeCapabilities;
}

/** A permission request waiting for an answer. */
export interface OpenPermission {
  deviceId: string;
  requestId: string;
  tool: string;
  toolUseId?: string;
  input: unknown;
  planExit?: PlanExit;
  at: number;
  /**
   * The machine is offline (ADR 0039): the request stays open and its clock is paused; `remainingMs` is the time
   * it had left. The machine's report after it reconnects opens it again, or closes it when it is gone.
   */
  unreachable?: boolean;
  remainingMs?: number;
}

/** A repeated `promptId` returns the first result for this long. */
export const PROMPT_DEDUP_MS = 10 * 60_000;
/**
 * A permission request nobody answered is closed here after this long; the agent denies it at the same time.
 * A plan exit (ADR 0019) never expires: it waits for the user until the turn is over.
 */
export const PERMISSION_OPEN_MS = 10 * 60_000;
/** Rule decisions of a session kept for browsers that subscribe later. */
export const MAX_RULE_LINES = 200;

export type Runtime = Pick<
  Control,
  | 'prompt'
  | 'answerPermission'
  | 'interrupt'
  | 'stop'
  | 'config'
  | 'runtimeOf'
  | 'openPermissions'
  | 'ruleLinesOf'
  | 'commandsOf'
> & {
  receive(
    link: DeviceLink,
    msg: Extract<
      ControlReply,
      {
        type:
          | 'runtime.state'
          | 'runtime.output'
          | 'runtime.permission'
          | 'runtime.permission.auto'
          | 'runtime.permission.closed'
          | 'runtime.rules.unenforced'
          | 'runtime.rejected'
          | 'runtime.steered'
          | 'runtime.commands';
      }
    >,
  ): void;
  /**
   * The device reconnected (ADR 0039): what the core held from before stays until the device's
   * `runtime.reported`, and what it replays meanwhile is counted for the summary.
   */
  attached(deviceId: string): void;
  /**
   * These devices lost their connection: their sessions are `offline` with the state they were in, and their
   * permission requests stay open as unreachable with their clocks paused (ADR 0039).
   */
  detached(deviceIds: string[], link: DeviceLink): void;
  /**
   * The device replayed its journal, sent its backlog and reported every running session: what it did not
   * report is over. Returns the sessions of the device that are idle or ready, whose queues may resume.
   */
  reported(deviceId: string): string[];
  /** The transcript bytes the device has still to send per session after a reconnect. */
  backlog(deviceId: string, sessions: DeviceBacklogSession[]): void;
  /** The store took `bytes` bytes of the session's transcript. */
  appended(sessionId: string, bytes: number): void;
};

/** The actor of an interrupt or a stop; the core's own commands are on the channel `core`. */
function actorFields(actor: CommandActor | undefined) {
  if (actor === undefined) return {};
  return actor.kind === 'core' ? { actor, channel: 'core' as const } : { actor };
}

function deviceOf(state: ControlState, link: DeviceLink): string | null {
  for (const [deviceId, attached] of state.byDevice) if (attached === link) return deviceId;
  return null;
}

/** States in which a session has no turn: its open permission requests are over. */
const TURN_OVER: ReadonlySet<AgentRuntimeState> = new Set([
  'idle',
  'starting',
  'ready',
  'failed',
  'external',
]);

/** States of a session whose process is up under Hoplet's control: leaving them for `idle` ends the session. */
const LIVE: ReadonlySet<AgentRuntimeState> = new Set([
  'starting',
  'ready',
  'running',
  'waiting-permission',
]);

/** A sender's title as the target's agent quotes it: one line, no quotes, at most 100 characters. */
export function senderTitle(raw: string): string {
  const title = raw
    .replace(/[\p{Cc}\p{Cf}\u2028\u2029"]+/gu, ' ')
    .replace(/\s+/g, ' ')
    .trim();
  return [...title].slice(0, 100).join('');
}

/**
 * The runtime state per session, prompt deduplication and permission routing (docs/spec.md#remote-control).
 * Runtime state lives in memory: agents rebuild it when they connect.
 */
export function createRuntime(state: ControlState): Runtime {
  const { store, runtime, permissions, promptCache, pendingPrompt } = state;

  function ownerSends(sessionId: string, deviceId: string): boolean {
    const owner = store.ownerDeviceId(sessionId);
    if (owner) {
      state.startedSessions.delete(sessionId);
      return owner === deviceId;
    }
    // A session the agent just started reaches the store with its first append.
    return state.startedSessions.get(sessionId)?.deviceId === deviceId;
  }

  function view(sessionId: string): RuntimeView {
    const owner = store.ownerDeviceId(sessionId) ?? state.startedSessions.get(sessionId)?.deviceId;
    if (!owner || state.byDevice.get(owner)?.isOpen() !== true) {
      // What the session was doing when its machine went away: it may still be doing it (ADR 0039).
      const entry = runtime.get(sessionId);
      const since = owner ? state.offlineSince.get(owner) : undefined;
      return {
        state: 'offline',
        ...capabilitiesOf(sessionId),
        ...(entry && entry.deviceId === owner ? { lastState: entry.state } : {}),
        ...(since !== undefined ? { since } : {}),
      };
    }
    const catchUp = state.catchUp.get(sessionId);
    const progress =
      catchUp && catchUp.deviceId === owner
        ? { catchUp: { received: catchUp.received, total: catchUp.total } }
        : {};
    const entry = runtime.get(sessionId);
    if (!entry || entry.deviceId !== owner) return { state: 'idle', ...progress };
    return {
      state: entry.state,
      ...(entry.error !== undefined ? { error: entry.error } : {}),
      capabilities: entry.capabilities,
      ...progress,
    };
  }

  function capabilitiesOf(sessionId: string): { capabilities?: RuntimeCapabilities } {
    const entry = runtime.get(sessionId);
    return entry ? { capabilities: entry.capabilities } : {};
  }

  function publishRuntime(sessionId: string): void {
    const v = view(sessionId);
    state.publish({
      v: PROTOCOL_VERSION,
      type: 'session.runtime',
      sessionId,
      state: v.state,
      ...(v.error !== undefined ? { error: v.error } : {}),
      ...(v.capabilities ? { capabilities: v.capabilities } : {}),
      ...(v.lastState !== undefined ? { lastState: v.lastState } : {}),
      ...(v.since !== undefined ? { since: v.since } : {}),
      ...(v.catchUp ? { catchUp: v.catchUp } : {}),
    });
  }

  /** A paused request never expires here: its machine counts its time (ADR 0039). */
  function permissionExpired(open: OpenPermission): boolean {
    return (
      open.planExit === undefined &&
      open.unreachable !== true &&
      state.now() - open.at > PERMISSION_OPEN_MS
    );
  }

  /** The browser's view of an open request. */
  function permissionMessage(sessionId: string, open: OpenPermission): SessionPermission {
    return {
      v: PROTOCOL_VERSION,
      type: 'session.permission',
      sessionId,
      requestId: open.requestId,
      tool: open.tool,
      ...(open.toolUseId !== undefined ? { toolUseId: open.toolUseId } : {}),
      input: open.input,
      ...(open.planExit ? { planExit: open.planExit } : {}),
      ...(open.unreachable ? { unreachable: true } : {}),
      requestedAt: open.at,
    };
  }

  /** Tells the listeners a request waits for an answer; a replayed one says when it was made. */
  function emitPermission(sessionId: string, open: OpenPermission, replayed: boolean): void {
    state.emit({
      kind: 'permission',
      sessionId,
      requestId: open.requestId,
      tool: open.tool,
      input: open.input,
      plan: open.planExit !== undefined,
      ...(open.planExit === undefined ? { expiresAt: open.at + PERMISSION_OPEN_MS } : {}),
      ...(replayed ? { replayed: true, at: open.at } : {}),
    });
  }

  /** The reconnect a replayed event of the device counts toward, if the device is between attach and report. */
  function reconnectOf(deviceId: string): Reconnect | undefined {
    return state.reconnects.get(deviceId);
  }

  function dropPermissions(sessionId: string): void {
    closeSessionPermissions(state, sessionId, 'abandoned');
  }

  function sweepPrompts(now: number): void {
    for (const [key, cached] of promptCache) {
      if (now - cached.at > PROMPT_DEDUP_MS) promptCache.delete(key);
    }
  }

  /** A prompt waits for the first state in which the agent has taken it. */
  function acceptPrompt(sessionId: string): void {
    for (const [key, waiter] of pendingPrompt) {
      if (waiter.sessionId !== sessionId) continue;
      const promptId = key.slice(key.indexOf('\n') + 1);
      waiter.resolve({ ok: true, promptId });
      clearTimeout(waiter.timer);
      pendingPrompt.delete(key);
    }
  }

  function rejectPrompt(sessionId: string, promptId: string, reason: string): void {
    const key = permissionKey(sessionId, promptId);
    const waiter = pendingPrompt.get(key);
    if (!waiter) return;
    pendingPrompt.delete(key);
    clearTimeout(waiter.timer);
    const known = reason === 'busy' || reason === 'starting' || reason === 'external';
    waiter.resolve(
      known
        ? { ok: false, error: reason, promptId }
        : { ok: false, error: 'rejected', promptId, reason },
    );
  }

  /** The lifecycle moments a state report makes (ADR 0031): a turn done, a process ended, a process that failed. */
  function trackLifecycle(
    sessionId: string,
    before: AgentRuntimeState | undefined,
    now: AgentRuntimeState,
    error: string | undefined,
    replay: { at: number; reconnect: Reconnect } | undefined,
  ): void {
    const at = replay?.at ?? state.now();
    // A moment the machine replayed happened while it was away (ADR 0039): hooks hear when, and notifications
    // leave it to the summary.
    const timing = replay ? { at, replayed: true } : {};
    const inTurn = before === 'running' || before === 'waiting-permission';
    const turns = state.turns.get(sessionId) ?? { count: 0 };
    if (now === 'running' && !inTurn) {
      turns.startedAt = at;
      state.turns.set(sessionId, turns);
    }
    if (now === 'ready' && inTurn) {
      turns.count += 1;
      const durationMs = turns.startedAt === undefined ? 0 : Math.max(0, at - turns.startedAt);
      delete turns.startedAt;
      state.turns.set(sessionId, turns);
      if (replay) replay.reconnect.turns += 1;
      state.emit({ kind: 'turn-done', sessionId, turn: turns.count, durationMs, ...timing });
    }
    if (now === 'failed') {
      state.emit({
        kind: 'runtime-error',
        sessionId,
        code: 'runtime-failed',
        ...(error !== undefined ? { reason: error } : {}),
        ...timing,
      });
    }
    if (now === 'idle') {
      const stopped = state.stopping.delete(sessionId);
      if (before !== undefined && LIVE.has(before)) {
        if (replay) replay.reconnect.ended += 1;
        state.emit({ kind: 'ended', sessionId, reason: stopped ? 'stopped' : 'idle', ...timing });
      }
    }
  }

  return {
    prompt(sessionId, ownerDeviceId, text, promptId, attachments = [], actor) {
      const now = state.now();
      sweepPrompts(now);
      const key = permissionKey(sessionId, promptId);
      const cached = promptCache.get(key);
      if (cached) return cached.outcome;

      const link = onlineLink(state, ownerDeviceId);
      if (!link) return Promise.resolve({ ok: false, error: 'owner-offline', promptId });
      const current = view(sessionId).state;
      if (current === 'external')
        return Promise.resolve({ ok: false, error: 'external', promptId });
      if (current === 'starting')
        return Promise.resolve({ ok: false, error: 'starting', promptId });
      if (
        current === 'running' ||
        current === 'waiting-permission' ||
        state.handoffs.has(sessionId)
      ) {
        return Promise.resolve({ ok: false, error: 'busy', promptId });
      }

      // A budget at its limit with `pause` refuses the prompt; a turn already running is never touched.
      const budget = state.store.budgetBlockForSession(sessionId, now);
      if (budget) {
        log('info', 'control', 'prompt refused by a budget', { sessionId, budget: budget.id });
        return Promise.resolve({ ok: false, error: 'over-budget', promptId, budget });
      }

      // Provenance (ADR 0034): the chain of sessions behind this prompt. A prompt from a session extends that
      // session's own chain; any other prompt starts a new one.
      const chain = actor?.kind === 'session' ? [...store.sessionChain(actor.id), actor.id] : [];
      store.setSessionChain(sessionId, chain);

      // The audit trail: who was asked to run what, never the text itself (docs/threat-model.md).
      store.audit({
        type: 'prompt.sent',
        ...(actor ? { actor } : {}),
        sessionId,
        deviceId: ownerDeviceId,
        ref: promptId,
        summary: {
          promptId,
          length: [...text].length,
          images: attachments.length,
          ...(chain.length > 0 ? { source: { kind: 'session', sessionId: actor?.id, chain } } : {}),
        },
      });
      log('info', 'control', 'prompt forwarded', {
        sessionId,
        promptId,
        images: attachments.length,
      });
      const msg: RuntimePrompt = {
        v: PROTOCOL_VERSION,
        type: 'runtime.prompt',
        sessionId,
        promptId,
        text,
        ...(attachments.length > 0 ? { attachments } : {}),
        // The target's agent puts a first line naming the sender in front of the text (ADR 0034).
        ...(actor?.kind === 'session'
          ? {
              source: {
                kind: 'session' as const,
                sessionId: actor.id,
                title: senderTitle(store.getSession(actor.id)?.title ?? ''),
                chain,
              },
            }
          : {}),
      };
      const outcome = askWaiter(
        pendingPrompt,
        key,
        state.promptTimeoutMs,
        { ok: false, error: 'timeout', promptId },
        () => {
          // The session's terminal ends first: the turn and a shell never share the directory (ADR 0026).
          state.closeTerminals(sessionId, 'turn-started');
          link.send(msg);
        },
        { link, sessionId },
      );
      promptCache.set(key, { at: now, outcome });
      // Only a result that may have reached the agent is repeated; a refusal can be retried.
      void outcome.then((result) => {
        if (!result.ok && result.error !== 'timeout') promptCache.delete(key);
      });
      return outcome;
    },

    answerPermission(sessionId, ownerDeviceId, requestId, answer: PermissionAnswer) {
      const { decision, message, mode } = answer;
      const link = onlineLink(state, ownerDeviceId);
      if (!link) return { ok: false, error: 'owner-offline' };
      const key = permissionKey(sessionId, requestId);
      const open = permissions.get(key);
      if (!open || open.deviceId !== ownerDeviceId || permissionExpired(open)) {
        if (open && open.deviceId === ownerDeviceId) closePermission(state, key, 'expired');
        else if (open) permissions.delete(key);
        return { ok: false, error: 'request-closed' };
      }
      // Answerable once the machine's report after its reconnect says the request still waits (ADR 0039).
      if (open.unreachable) return { ok: false, error: 'owner-offline' };
      // A mode is only the approval of a plan, and only one the request offers; the request stays open.
      if (mode !== undefined && (decision !== 'allow' || !open.planExit?.modes.includes(mode))) {
        return { ok: false, error: 'invalid-request' };
      }
      // The record comes first: an answer the audit log cannot hold is not sent (ADR 0030).
      store.audit({
        type: 'permission.answered',
        ...(answer.actor ? { actor: answer.actor } : {}),
        sessionId,
        deviceId: ownerDeviceId,
        ref: requestId,
        channel: answer.by === 'link' ? 'link' : 'rest',
        summary: {
          decision,
          answeredBy: answer.actor ? 'token' : (answer.by ?? 'operator'),
          tool: open.tool,
          ...(mode !== undefined ? { mode } : {}),
        },
      });
      // The first answer wins: the request is gone before the answer is sent.
      closePermission(state, key, decision, {
        kind: answer.by === 'link' ? 'action-link' : 'user',
      });
      log('info', 'control', 'permission answered', { sessionId, requestId, decision });
      const msg: RuntimePermissionAnswer = {
        v: PROTOCOL_VERSION,
        type: 'runtime.permission.answer',
        sessionId,
        requestId,
        decision,
        ...(message !== undefined && message !== '' ? { message } : {}),
        ...(mode !== undefined ? { mode } : {}),
      };
      link.send(msg);
      return { ok: true };
    },

    interrupt(sessionId, ownerDeviceId, actor): RuntimeCommandOutcome {
      const link = onlineLink(state, ownerDeviceId);
      if (!link) return { ok: false, error: 'owner-offline' };
      const current = view(sessionId).state;
      if (current !== 'running' && current !== 'waiting-permission') {
        return { ok: false, error: 'not-running' };
      }
      store.audit({
        type: 'session.interrupted',
        ...actorFields(actor),
        sessionId,
        deviceId: ownerDeviceId,
      });
      log('info', 'control', 'interrupt forwarded', { sessionId });
      const msg: RuntimeInterrupt = { v: PROTOCOL_VERSION, type: 'runtime.interrupt', sessionId };
      link.send(msg);
      return { ok: true };
    },

    stop(sessionId, ownerDeviceId, actor, cascadeOf): RuntimeCommandOutcome {
      const link = onlineLink(state, ownerDeviceId);
      if (!link) return { ok: false, error: 'owner-offline' };
      store.audit({
        type: 'session.stopped',
        ...actorFields(actor),
        sessionId,
        deviceId: ownerDeviceId,
        ...(cascadeOf !== undefined ? { summary: { cascade: cascadeOf } } : {}),
      });
      log('info', 'control', 'stop forwarded', { sessionId });
      state.stopping.add(sessionId);
      const msg: RuntimeStop = { v: PROTOCOL_VERSION, type: 'runtime.stop', sessionId };
      link.send(msg);
      return { ok: true };
    },

    config(sessionId, ownerDeviceId, configId, value: ConfigSetting): RuntimeCommandOutcome {
      const link = onlineLink(state, ownerDeviceId);
      if (!link) return { ok: false, error: 'owner-offline' };
      if (view(sessionId).state === 'external') return { ok: false, error: 'external' };
      store.audit({
        type: 'mode.changed',
        sessionId,
        deviceId: ownerDeviceId,
        summary: { option: configId, value },
      });
      log('info', 'control', 'config forwarded', { sessionId, configId, value });
      const msg: RuntimeConfig = {
        v: PROTOCOL_VERSION,
        type: 'runtime.config',
        sessionId,
        configId,
        value,
      };
      link.send(msg);
      return { ok: true };
    },

    runtimeOf: view,

    commandsOf(sessionId) {
      const entry = state.commands.get(sessionId);
      const owner =
        store.ownerDeviceId(sessionId) ?? state.startedSessions.get(sessionId)?.deviceId;
      if (!entry || entry.deviceId !== owner) return null;
      return entry.commands;
    },

    openPermissions(sessionId) {
      const out: SessionPermission[] = [];
      for (const [key, open] of permissions) {
        if (!key.startsWith(`${sessionId}\n`) || permissionExpired(open)) continue;
        out.push(permissionMessage(sessionId, open));
      }
      return out;
    },

    ruleLinesOf: (sessionId) => state.ruleLines.get(sessionId) ?? [],

    receive(link, msg) {
      const deviceId = deviceOf(state, link);
      if (!deviceId) return;
      if (!ownerSends(msg.sessionId, deviceId)) {
        // Before the answer to a start the session has no name here; the agent reports everything again
        // once it has answered (sync/control.go), so this is expected.
        for (const waiter of state.pendingStart.values()) if (waiter.link === link) return;
        log('warn', 'control', 'runtime message from a device that does not own the session', {
          sessionId: msg.sessionId,
          type: msg.type,
        });
        return;
      }
      const reconnect = reconnectOf(deviceId);
      const replay =
        'replayed' in msg && msg.replayed === true && reconnect
          ? { at: msg.at ?? state.now(), reconnect }
          : undefined;
      switch (msg.type) {
        case 'runtime.state': {
          const before = runtime.get(msg.sessionId)?.state;
          reconnect?.reported.add(msg.sessionId);
          runtime.set(msg.sessionId, {
            deviceId,
            state: msg.state,
            ...(msg.error !== undefined ? { error: msg.error } : {}),
            capabilities: msg.capabilities,
          });
          if (TURN_OVER.has(msg.state)) dropPermissions(msg.sessionId);
          if (msg.state === 'starting' || msg.state === 'running') acceptPrompt(msg.sessionId);
          publishRuntime(msg.sessionId);
          state.runtimeChanged(msg.sessionId, msg.state, msg.error);
          trackLifecycle(msg.sessionId, before, msg.state, msg.error, replay);
          return;
        }
        case 'runtime.permission.closed': {
          // The machine denied a request nobody answered in time (ADR 0039).
          closePermission(
            state,
            permissionKey(msg.sessionId, msg.requestId),
            'expired',
            undefined,
            'timeout',
          );
          return;
        }
        case 'runtime.output': {
          const { type: _type, v: _v, ...rest } = msg;
          state.publish({ v: PROTOCOL_VERSION, type: 'session.output', ...rest });
          return;
        }
        case 'runtime.permission.auto': {
          // A rule decided; the call is settled already, so a failed record is told and nothing else happens.
          try {
            store.audit({
              type: 'permission.auto',
              ...(msg.at !== undefined ? { at: msg.at } : {}),
              sessionId: msg.sessionId,
              deviceId,
              actor: { kind: 'device', id: deviceId },
              channel: 'agent',
              ref: msg.toolUseId ?? null,
              summary: {
                decision: msg.decision,
                ruleId: msg.ruleId,
                scope: msg.scope,
                revision: msg.revision,
              },
            });
          } catch (err) {
            log('error', 'control', 'rule decision not recorded', {
              sessionId: msg.sessionId,
              error: err instanceof Error ? err.message : 'unknown',
            });
          }
          if (msg.toolUseId !== undefined) {
            // Which rule decided is shown beside the call; only ids travel, the browser finds the name.
            const line: SessionRule = {
              v: PROTOCOL_VERSION,
              type: 'session.rule',
              sessionId: msg.sessionId,
              toolUseId: msg.toolUseId,
              decision: msg.decision,
              ruleId: msg.ruleId,
              scope: msg.scope,
            };
            const lines = [...(state.ruleLines.get(msg.sessionId) ?? []), line];
            state.ruleLines.set(msg.sessionId, lines.slice(-MAX_RULE_LINES));
            state.publish(line);
          }
          return;
        }
        case 'runtime.rules.unenforced': {
          // Told at the conversation, and again to a browser that subscribes later.
          const line: SessionRulesUnenforced = {
            v: PROTOCOL_VERSION,
            type: 'session.rules.unenforced',
            sessionId: msg.sessionId,
            ...(msg.toolUseId !== undefined ? { toolUseId: msg.toolUseId } : {}),
          };
          const lines = [...(state.ruleLines.get(msg.sessionId) ?? []), line];
          state.ruleLines.set(msg.sessionId, lines.slice(-MAX_RULE_LINES));
          state.publish(line);
          return;
        }
        case 'runtime.permission': {
          const key = permissionKey(msg.sessionId, msg.requestId);
          const known = permissions.get(key);
          if (known && known.deviceId === deviceId) {
            // The same request again, from a replay or the report after a reconnect (ADR 0039): it opens again
            // with the time it had left, and is neither recorded nor notified a second time.
            if (known.unreachable) {
              const remaining = known.remainingMs ?? PERMISSION_OPEN_MS;
              delete known.unreachable;
              delete known.remainingMs;
              if (known.planExit === undefined) {
                known.at = state.now() - (PERMISSION_OPEN_MS - remaining);
                armExpiry(state, key, remaining);
              }
              state.publish(permissionMessage(msg.sessionId, known));
            }
            return;
          }
          // The agent asked again under the same ID: the earlier request is over.
          closePermission(state, key, 'abandoned');
          try {
            const { command, paths } = toolInputSummary(msg.tool, msg.input);
            store.audit({
              type: 'permission.requested',
              ...(msg.at !== undefined ? { at: msg.at } : {}),
              sessionId: msg.sessionId,
              deviceId,
              actor: { kind: 'device', id: deviceId },
              channel: 'agent',
              ref: msg.requestId,
              summary: {
                tool: msg.tool,
                planExit: msg.planExit !== undefined,
                ...(command !== undefined ? { command } : {}),
                ...(paths.length > 0 ? { paths } : {}),
              },
            });
          } catch (err) {
            log('error', 'control', 'permission request not recorded', {
              sessionId: msg.sessionId,
              error: err instanceof Error ? err.message : 'unknown',
            });
          }
          const open: OpenPermission = {
            deviceId,
            requestId: msg.requestId,
            tool: msg.tool,
            ...(msg.toolUseId !== undefined ? { toolUseId: msg.toolUseId } : {}),
            input: msg.input,
            ...(msg.planExit ? { planExit: msg.planExit } : {}),
            at: state.now(),
          };
          permissions.set(key, open);
          if (msg.planExit === undefined) armExpiry(state, key, PERMISSION_OPEN_MS);
          state.publish(permissionMessage(msg.sessionId, open));
          // A replayed request is notified after the machine's report, if it still waits then (ADR 0039).
          if (replay) replay.reconnect.permissions.push(key);
          else emitPermission(msg.sessionId, open, false);
          return;
        }
        case 'runtime.rejected': {
          rejectPrompt(msg.sessionId, msg.promptId, msg.reason);
          const key = permissionKey(msg.sessionId, msg.promptId);
          const steer = state.pendingSteer.get(key);
          if (steer?.link === link) {
            const error =
              msg.reason === 'permission' || msg.reason === 'unsupported'
                ? 'steer-unavailable'
                : msg.reason === 'not-running' || msg.reason === 'busy'
                  ? 'not-running'
                  : 'rejected';
            settleWaiter(state.pendingSteer, key, {
              ok: false,
              error,
              ...(error === 'rejected' ? { reason: msg.reason } : {}),
            });
          }
          return;
        }
        case 'runtime.steered': {
          const key = permissionKey(msg.sessionId, msg.steerId);
          if (state.pendingSteer.get(key)?.link === link) {
            settleWaiter(state.pendingSteer, key, { ok: true });
          }
          return;
        }
        case 'runtime.commands': {
          // Only counts are logged: descriptions come from files of the user's projects.
          state.commands.set(msg.sessionId, { deviceId, commands: msg.commands });
          store.putDirectoryCommands(deviceId, msg.cwd, msg.commands);
          log('debug', 'control', 'commands reported', {
            sessionId: msg.sessionId,
            count: msg.commands.length,
          });
          state.publish({
            v: PROTOCOL_VERSION,
            type: 'session.commands',
            sessionId: msg.sessionId,
          });
          return;
        }
      }
    },

    attached(deviceId) {
      state.offlineSince.delete(deviceId);
      state.reconnects.set(deviceId, { reported: new Set(), turns: 0, ended: 0, permissions: [] });
      for (const [sessionId, entry] of runtime) {
        if (entry.deviceId === deviceId) publishRuntime(sessionId);
      }
    },

    reported(deviceId) {
      const reconnect = state.reconnects.get(deviceId);
      state.reconnects.delete(deviceId);
      // A process the machine did not report is not running any more.
      for (const [sessionId, entry] of [...runtime]) {
        if (entry.deviceId !== deviceId || reconnect?.reported.has(sessionId)) continue;
        runtime.delete(sessionId);
        publishRuntime(sessionId);
      }
      // A request the report did not name again is gone with its process.
      for (const [key, open] of [...permissions]) {
        if (open.deviceId === deviceId && open.unreachable) {
          closePermission(state, key, 'abandoned', undefined, 'session-end');
        }
      }
      if (reconnect) {
        for (const key of reconnect.permissions) {
          const open = permissions.get(key);
          if (open) emitPermission(key.slice(0, key.indexOf('\n')), open, true);
        }
        if (reconnect.turns > 0 || reconnect.ended > 0) {
          state.emit({
            kind: 'reconnected',
            deviceId,
            turns: reconnect.turns,
            ended: reconnect.ended,
          });
        }
      }
      const resumable: string[] = [];
      for (const session of store.listSessions(5000)) {
        if (session.ownerDeviceId !== deviceId) continue;
        const current = view(session.sessionId).state;
        if (current === 'idle' || current === 'ready') resumable.push(session.sessionId);
      }
      return resumable;
    },

    backlog(deviceId, sessions) {
      for (const { sessionId, bytes } of sessions) {
        if (bytes <= 0) continue;
        state.catchUp.set(sessionId, { deviceId, received: 0, total: bytes });
        publishRuntime(sessionId);
      }
    },

    appended(sessionId, bytes) {
      const catchUp = state.catchUp.get(sessionId);
      if (!catchUp) return;
      catchUp.received += bytes;
      if (catchUp.received >= catchUp.total) state.catchUp.delete(sessionId);
      publishRuntime(sessionId);
    },

    detached(deviceIds, link) {
      // A prompt still waiting for the agent that left will not be answered.
      for (const [key, waiter] of [...pendingPrompt]) {
        if (waiter.link !== link) continue;
        const promptId = key.slice(key.indexOf('\n') + 1);
        pendingPrompt.delete(key);
        clearTimeout(waiter.timer);
        waiter.resolve({ ok: false, error: 'owner-offline', promptId });
      }
      for (const [key, waiter] of [...state.pendingSteer]) {
        if (waiter.link === link)
          settleWaiter(state.pendingSteer, key, { ok: false, error: 'owner-offline' });
      }
      const gone = new Set(deviceIds);
      const since = state.now();
      for (const deviceId of deviceIds) {
        state.offlineSince.set(deviceId, since);
        state.reconnects.delete(deviceId);
      }
      for (const [sessionId, catchUp] of [...state.catchUp]) {
        if (gone.has(catchUp.deviceId)) state.catchUp.delete(sessionId);
      }
      for (const [sessionId, entry] of runtime) {
        if (gone.has(entry.deviceId)) publishRuntime(sessionId);
      }
      // The machine may still be running the turn and waiting for the answer (ADR 0039): the request stays
      // open, unanswerable, with its clock paused.
      for (const [key, open] of permissions) {
        if (!gone.has(open.deviceId) || open.unreachable) continue;
        const timer = state.permissionTimers.get(key);
        if (timer) {
          clearTimeout(timer);
          state.permissionTimers.delete(key);
        }
        open.unreachable = true;
        if (open.planExit === undefined) {
          open.remainingMs = Math.max(0, PERMISSION_OPEN_MS - (since - open.at));
        }
        state.publish(permissionMessage(key.slice(0, key.indexOf('\n')), open));
      }
    },
  };
}
