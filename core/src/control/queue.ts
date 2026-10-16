import { randomUUID } from 'node:crypto';
import {
  type AgentRuntimeState,
  PROTOCOL_VERSION,
  type Queue,
  type QueueItem,
  type RuntimeSteer,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import { askWaiter } from './pending.ts';
import type { Runtime } from './runtime.ts';
import { type ControlState, onlineLink } from './state.ts';
import type { Control, SteerOutcome } from './types.ts';

/** A repeated `steerId` returns the first result for this long. */
const STEER_DEDUP_MS = 10 * 60_000;

export type QueueControl = Pick<
  Control,
  | 'queueOf'
  | 'queueAdd'
  | 'queueEdit'
  | 'queueRemove'
  | 'queueReorder'
  | 'queueResume'
  | 'queueClear'
  | 'queueSteer'
  | 'steer'
> & {
  /** The runtime state of a session changed: a finished turn sends the next message. */
  runtimeChanged(sessionId: string, state: AgentRuntimeState, error?: string): void;
  /** The user interrupted the session's turn: the queue waits for an explicit resume. */
  interrupted(sessionId: string): void;
  /** Sends the head message if the session can take it. */
  drain(sessionId: string): void;
};

/**
 * The queue of next messages the core holds per session, and the steer of a running turn (ADR 0020). The queue
 * drains here: a turn that ended successfully sends the head message as the next prompt.
 */
export function createQueue(state: ControlState, runtime: Runtime): QueueControl {
  const { store } = state;
  /** Sessions whose head message is on its way to the agent. */
  const sending = new Set<string>();
  const steerCache = new Map<string, { at: number; outcome: Promise<SteerOutcome> }>();

  function changed(sessionId: string): Queue {
    state.publish({ v: PROTOCOL_VERSION, type: 'session.queue', sessionId });
    return store.queueOf(sessionId);
  }

  async function drain(sessionId: string): Promise<void> {
    if (sending.has(sessionId) || state.handoffs.has(sessionId)) return;
    const owner = store.ownerDeviceId(sessionId);
    if (!owner || !onlineLink(state, owner)) return;
    if (store.queueOf(sessionId).paused) return;
    const current = runtime.runtimeOf(sessionId).state;
    if (current !== 'idle' && current !== 'ready') return;
    const next = store.queueNext(sessionId);
    if (!next) return;
    const claimed = store.queueClaim(sessionId, next.id, next.version);
    if (!claimed.ok) return;
    sending.add(sessionId);
    changed(sessionId);
    let outcome: Awaited<ReturnType<Runtime['prompt']>>;
    try {
      outcome = await runtime.prompt(
        sessionId,
        owner,
        next.text,
        `q-${randomUUID()}`,
        next.attachments,
      );
    } finally {
      sending.delete(sessionId);
    }
    if (outcome.ok) {
      store.queueSettle(sessionId, next.id);
    } else {
      store.queueRestore(sessionId, next.id);
      // Busy, starting, external and an owner that left are states that pass; a refusal or no answer is not.
      if (outcome.error === 'rejected' || outcome.error === 'timeout') {
        store.queueSetPause(sessionId, 'error', outcome.reason ?? outcome.error);
      }
    }
    log('info', 'control', 'queued message drained', { sessionId, ok: outcome.ok });
    changed(sessionId);
    if (outcome.ok) void drain(sessionId);
  }

  function steer(
    sessionId: string,
    ownerDeviceId: string,
    steerId: string,
    text: string,
    attachments: string[] = [],
  ): Promise<SteerOutcome> {
    const now = state.now();
    for (const [key, cached] of steerCache) {
      if (now - cached.at > STEER_DEDUP_MS) steerCache.delete(key);
    }
    const key = `${sessionId}\n${steerId}`;
    const cached = steerCache.get(key);
    if (cached) return cached.outcome;

    const link = onlineLink(state, ownerDeviceId);
    if (!link) return Promise.resolve({ ok: false, error: 'owner-offline' });
    const view = runtime.runtimeOf(sessionId);
    if (view.state === 'waiting-permission') {
      return Promise.resolve({ ok: false, error: 'steer-unavailable' });
    }
    if (view.state !== 'running' || state.handoffs.has(sessionId)) {
      return Promise.resolve({ ok: false, error: 'not-running' });
    }
    if (view.capabilities?.steer !== true) {
      return Promise.resolve({ ok: false, error: 'steer-unavailable' });
    }
    // The audit trail: who was asked to steer, never the text itself (docs/threat-model.md).
    log('info', 'control', 'steer forwarded', { sessionId, steerId, images: attachments.length });
    const msg: RuntimeSteer = {
      v: PROTOCOL_VERSION,
      type: 'runtime.steer',
      sessionId,
      steerId,
      text,
      ...(attachments.length > 0 ? { attachments } : {}),
    };
    const outcome = askWaiter(
      state.pendingSteer,
      key,
      state.promptTimeoutMs,
      { ok: false, error: 'timeout' } as SteerOutcome,
      () => link.send(msg),
      { link, sessionId },
    );
    steerCache.set(key, { at: now, outcome });
    // Only a result that may have reached the agent is repeated; a refusal can be retried.
    void outcome.then((result) => {
      if (!result.ok && result.error !== 'timeout') steerCache.delete(key);
    });
    return outcome;
  }

  return {
    queueOf: (sessionId) => store.queueOf(sessionId),

    queueAdd(sessionId, input) {
      const added = store.queueAdd(sessionId, input);
      if (!added.ok) return { ok: false, error: added.reason };
      const queue = changed(sessionId);
      void drain(sessionId);
      return { ok: true, queue };
    },

    queueEdit(sessionId, id, version, input) {
      const edited = store.queueEdit(sessionId, id, version, input);
      return edited.ok
        ? { ok: true, queue: changed(sessionId) }
        : { ok: false, error: edited.reason };
    },

    queueRemove(sessionId, id, version) {
      const removed = store.queueRemove(sessionId, id, version);
      return removed.ok
        ? { ok: true, queue: changed(sessionId) }
        : { ok: false, error: removed.reason };
    },

    queueReorder(sessionId, ids) {
      const ordered = store.queueReorder(sessionId, ids);
      return ordered.ok
        ? { ok: true, queue: changed(sessionId) }
        : { ok: false, error: ordered.reason };
    },

    queueResume(sessionId) {
      store.queueSetPause(sessionId, null);
      const queue = changed(sessionId);
      void drain(sessionId);
      return queue;
    },

    queueClear(sessionId) {
      store.queueClear(sessionId);
      return changed(sessionId);
    },

    async queueSteer(sessionId, ownerDeviceId, id, version) {
      const item: QueueItem | undefined = store
        .queueOf(sessionId)
        .items.find((i) => i.id === id && i.version === version && i.state === 'queued');
      if (!item) return { ok: false, error: 'conflict' };
      // Nothing is claimed unless the steer can be sent: a refusal before the agent leaves the queue untouched.
      const claimed = store.queueClaim(sessionId, id, version);
      if (!claimed.ok) return { ok: false, error: 'conflict' };
      changed(sessionId);
      const outcome = await steer(
        sessionId,
        ownerDeviceId,
        `s-${randomUUID()}`,
        item.text,
        item.attachments,
      );
      if (outcome.ok) store.queueSettle(sessionId, id);
      else store.queueRestore(sessionId, id);
      return { ok: true, queue: changed(sessionId), steer: outcome };
    },

    steer,

    runtimeChanged(sessionId, current, error) {
      if (current === 'failed') {
        store.queueSetPause(sessionId, 'error', error);
        changed(sessionId);
        return;
      }
      if (current === 'idle' || current === 'ready') void drain(sessionId);
    },

    interrupted(sessionId) {
      if (store.queueOf(sessionId).items.length === 0) return;
      store.queueSetPause(sessionId, 'interrupted');
      changed(sessionId);
    },

    drain(sessionId) {
      void drain(sessionId);
    },
  };
}
