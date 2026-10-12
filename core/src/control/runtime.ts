import {
  type AgentRuntimeState,
  type PermissionDecision,
  PROTOCOL_VERSION,
  type RuntimeCapabilities,
  type RuntimeInterrupt,
  type RuntimePermissionAnswer,
  type RuntimePrompt,
  type RuntimeStop,
  type SessionPermission,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import { askWaiter } from './pending.ts';
import { type ControlState, onlineLink } from './state.ts';
import type {
  Control,
  ControlReply,
  DeviceLink,
  RuntimeCommandOutcome,
  RuntimeView,
} from './types.ts';

/** The state of one session's process as its owner reported it. */
export interface RuntimeEntry {
  deviceId: string;
  state: AgentRuntimeState;
  error?: string;
  capabilities: RuntimeCapabilities;
}

/** A permission request waiting for an answer. */
export interface OpenPermission {
  deviceId: string;
  requestId: string;
  tool: string;
  toolUseId?: string;
  input: unknown;
  at: number;
}

/** A repeated `promptId` returns the first result for this long. */
export const PROMPT_DEDUP_MS = 10 * 60_000;
/** A permission request nobody answered is closed here after this long; the agent denies it at the same time. */
export const PERMISSION_OPEN_MS = 10 * 60_000;

export type Runtime = Pick<
  Control,
  'prompt' | 'answerPermission' | 'interrupt' | 'stop' | 'runtimeOf' | 'openPermissions'
> & {
  receive(
    link: DeviceLink,
    msg: Extract<
      ControlReply,
      { type: 'runtime.state' | 'runtime.output' | 'runtime.permission' | 'runtime.rejected' }
    >,
  ): void;
  /** The device reconnected: what it ran before is unknown until it reports again. */
  attached(deviceId: string): void;
  /** These devices lost their connection: their sessions are `offline`. */
  detached(deviceIds: string[], link: DeviceLink): void;
};

function permissionKey(sessionId: string, requestId: string): string {
  return `${sessionId}\n${requestId}`;
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
    const owner = store.ownerDeviceId(sessionId);
    if (!owner || state.byDevice.get(owner)?.isOpen() !== true) {
      return { state: 'offline', ...capabilitiesOf(sessionId) };
    }
    const entry = runtime.get(sessionId);
    if (!entry || entry.deviceId !== owner) return { state: 'idle' };
    return {
      state: entry.state,
      ...(entry.error !== undefined ? { error: entry.error } : {}),
      capabilities: entry.capabilities,
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
    });
  }

  function dropPermissions(sessionId: string): void {
    for (const key of [...permissions.keys()]) {
      if (key.startsWith(`${sessionId}\n`)) permissions.delete(key);
    }
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

  return {
    prompt(sessionId, ownerDeviceId, text, promptId) {
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

      // The audit trail: who was asked to run what, never the text itself (docs/threat-model.md).
      log('info', 'control', 'prompt forwarded', { sessionId, promptId });
      const msg: RuntimePrompt = {
        v: PROTOCOL_VERSION,
        type: 'runtime.prompt',
        sessionId,
        promptId,
        text,
      };
      const outcome = askWaiter(
        pendingPrompt,
        key,
        state.promptTimeoutMs,
        { ok: false, error: 'timeout', promptId },
        () => link.send(msg),
        { link, sessionId },
      );
      promptCache.set(key, { at: now, outcome });
      // Only a result that may have reached the agent is repeated; a refusal can be retried.
      void outcome.then((result) => {
        if (!result.ok && result.error !== 'timeout') promptCache.delete(key);
      });
      return outcome;
    },

    answerPermission(sessionId, ownerDeviceId, requestId, decision: PermissionDecision) {
      const link = onlineLink(state, ownerDeviceId);
      if (!link) return { ok: false, error: 'owner-offline' };
      const key = permissionKey(sessionId, requestId);
      const open = permissions.get(key);
      // The first answer wins: the request is gone before the answer is sent.
      permissions.delete(key);
      if (!open || open.deviceId !== ownerDeviceId || state.now() - open.at > PERMISSION_OPEN_MS) {
        return { ok: false, error: 'request-closed' };
      }
      log('info', 'control', 'permission answered', { sessionId, requestId, decision });
      const msg: RuntimePermissionAnswer = {
        v: PROTOCOL_VERSION,
        type: 'runtime.permission.answer',
        sessionId,
        requestId,
        decision,
      };
      link.send(msg);
      return { ok: true };
    },

    interrupt(sessionId, ownerDeviceId): RuntimeCommandOutcome {
      const link = onlineLink(state, ownerDeviceId);
      if (!link) return { ok: false, error: 'owner-offline' };
      const current = view(sessionId).state;
      if (current !== 'running' && current !== 'waiting-permission') {
        return { ok: false, error: 'not-running' };
      }
      log('info', 'control', 'interrupt forwarded', { sessionId });
      const msg: RuntimeInterrupt = { v: PROTOCOL_VERSION, type: 'runtime.interrupt', sessionId };
      link.send(msg);
      return { ok: true };
    },

    stop(sessionId, ownerDeviceId): RuntimeCommandOutcome {
      const link = onlineLink(state, ownerDeviceId);
      if (!link) return { ok: false, error: 'owner-offline' };
      log('info', 'control', 'stop forwarded', { sessionId });
      const msg: RuntimeStop = { v: PROTOCOL_VERSION, type: 'runtime.stop', sessionId };
      link.send(msg);
      return { ok: true };
    },

    runtimeOf: view,

    openPermissions(sessionId) {
      const out: SessionPermission[] = [];
      const now = state.now();
      for (const [key, open] of permissions) {
        if (!key.startsWith(`${sessionId}\n`) || now - open.at > PERMISSION_OPEN_MS) continue;
        out.push({
          v: PROTOCOL_VERSION,
          type: 'session.permission',
          sessionId,
          requestId: open.requestId,
          tool: open.tool,
          ...(open.toolUseId !== undefined ? { toolUseId: open.toolUseId } : {}),
          input: open.input,
        });
      }
      return out;
    },

    receive(link, msg) {
      const deviceId = deviceOf(state, link);
      if (!deviceId) return;
      if (!ownerSends(msg.sessionId, deviceId)) {
        log('warn', 'control', 'runtime message from a device that does not own the session', {
          sessionId: msg.sessionId,
          type: msg.type,
        });
        return;
      }
      switch (msg.type) {
        case 'runtime.state': {
          runtime.set(msg.sessionId, {
            deviceId,
            state: msg.state,
            ...(msg.error !== undefined ? { error: msg.error } : {}),
            capabilities: msg.capabilities,
          });
          if (TURN_OVER.has(msg.state)) dropPermissions(msg.sessionId);
          if (msg.state === 'starting' || msg.state === 'running') acceptPrompt(msg.sessionId);
          publishRuntime(msg.sessionId);
          return;
        }
        case 'runtime.output': {
          const { type: _type, v: _v, ...rest } = msg;
          state.publish({ v: PROTOCOL_VERSION, type: 'session.output', ...rest });
          return;
        }
        case 'runtime.permission': {
          permissions.set(permissionKey(msg.sessionId, msg.requestId), {
            deviceId,
            requestId: msg.requestId,
            tool: msg.tool,
            ...(msg.toolUseId !== undefined ? { toolUseId: msg.toolUseId } : {}),
            input: msg.input,
            at: state.now(),
          });
          const { type: _type, v: _v, ...rest } = msg;
          state.publish({ v: PROTOCOL_VERSION, type: 'session.permission', ...rest });
          return;
        }
        case 'runtime.rejected':
          rejectPrompt(msg.sessionId, msg.promptId, msg.reason);
          return;
      }
    },

    attached(deviceId) {
      const reset: string[] = [];
      for (const [sessionId, entry] of runtime) {
        if (entry.deviceId !== deviceId) continue;
        runtime.delete(sessionId);
        reset.push(sessionId);
      }
      for (const [key, open] of permissions)
        if (open.deviceId === deviceId) permissions.delete(key);
      for (const sessionId of reset) publishRuntime(sessionId);
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
      const gone = new Set(deviceIds);
      for (const [sessionId, entry] of runtime) {
        if (gone.has(entry.deviceId)) publishRuntime(sessionId);
      }
      for (const [key, open] of permissions) if (gone.has(open.deviceId)) permissions.delete(key);
    },
  };
}
