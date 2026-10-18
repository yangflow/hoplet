import { randomUUID } from 'node:crypto';
import {
  type DeleteSessionRequest,
  type DeletionPreview,
  type DeletionStep,
  PROTOCOL_VERSION,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import { askWaiter } from './pending.ts';
import { type ControlState, onlineLink } from './state.ts';
import type { Control, SessionDeviceOutcome } from './types.ts';

type DeviceSessionRequest =
  | { type: 'device.session.inspect' }
  | {
      type: 'device.session.delete';
      vendorFile: boolean;
      worktree: boolean;
      worktreeDirty: boolean;
    }
  | { type: 'device.session.forget' };

export type DeletionControl = Pick<Control, 'deletionPreview' | 'deleteSession'> & {
  /** The device connected: tells it to forget the sessions deleted while it was away. */
  attached(deviceId: string): void;
};

/** A turn is on, a process is warming up or a handoff is moving the session: it cannot be deleted. */
const NOT_RUNNING: ReadonlySet<string> = new Set(['idle', 'failed', 'external', 'offline']);

/**
 * Deleting a session and what Hoplet made for it (ADR 0023). The core writes the tombstone, asks the owner agent
 * to do its steps, and removes its own copy last; the agent finds the vendor file and the worktree from the
 * session ID, so no path travels.
 */
export function createDeletion(
  state: ControlState,
  runtimeOf: Control['runtimeOf'],
): DeletionControl {
  const { store, pendingSession } = state;

  function askAgent(
    deviceId: string,
    sessionId: string,
    request: DeviceSessionRequest,
  ): Promise<SessionDeviceOutcome> {
    const link = onlineLink(state, deviceId);
    if (!link) return Promise.resolve({ ok: false, error: 'owner-offline' });
    const requestId = randomUUID();
    return askWaiter(
      pendingSession,
      requestId,
      state.workspaceTimeoutMs,
      { ok: false, error: 'timeout' },
      () => link.send({ v: PROTOCOL_VERSION, requestId, sessionId, ...request }),
      { link, sessionId },
    );
  }

  function isRunning(sessionId: string): boolean {
    return state.handoffs.has(sessionId) || !NOT_RUNNING.has(runtimeOf(sessionId).state);
  }

  /** Drops what the core holds in memory for a session that is going away. */
  function forgetInMemory(sessionId: string): void {
    state.closeTerminals(sessionId, 'deleted');
    state.sessionPorts.delete(sessionId);
    state.runtime.delete(sessionId);
    state.commands.delete(sessionId);
    state.startedSessions.delete(sessionId);
    for (const key of [...state.permissions.keys()]) {
      if (key.startsWith(`${sessionId}\n`)) state.permissions.delete(key);
    }
  }

  function nameOf(deviceId: string): string {
    return store.listDevices().find((d) => d.deviceId === deviceId)?.name ?? deviceId;
  }

  return {
    async deletionPreview(sessionId) {
      const owner = store.deletingOwner(sessionId);
      if (!owner) return { ok: false, error: 'session-not-found' };
      const record = store.sessionWorktree(sessionId);
      const preview: DeletionPreview = {
        owner: { deviceId: owner, name: nameOf(owner), online: onlineLink(state, owner) !== null },
        running: isRunning(sessionId),
        vendorFile: null,
        worktree: null,
        previousOwner:
          record && record.fromDeviceId !== owner
            ? { deviceId: record.fromDeviceId, name: nameOf(record.fromDeviceId) }
            : null,
      };
      if (!preview.owner.online) return { ok: true, preview };
      const answer = await askAgent(owner, sessionId, { type: 'device.session.inspect' });
      if (!answer.ok) {
        return {
          ok: false,
          error: answer.error,
          ...(answer.reason !== undefined ? { reason: answer.reason } : {}),
        };
      }
      if (answer.reply.type !== 'device.session.inspected') {
        return { ok: false, error: 'rejected', reason: 'unexpected-reply' };
      }
      preview.vendorFile = answer.reply.vendorFile ?? null;
      // The core's record decides whether a worktree is offered at all; the agent confirms it is still there.
      preview.worktree = record ? (answer.reply.worktree ?? null) : null;
      return { ok: true, preview };
    },

    async deleteSession(sessionId, request: DeleteSessionRequest) {
      const owner = store.deletingOwner(sessionId);
      if (!owner) return { ok: false, error: 'session-not-found' };
      if (isRunning(sessionId)) return { ok: false, error: 'session-running' };
      const wantsAgent = request.vendorFile || request.worktree;
      const online = onlineLink(state, owner) !== null;
      if (wantsAgent && !online) return { ok: false, error: 'owner-offline' };

      // The tombstone first: from here the session is in no list and ingest refuses it.
      if (!store.isTombstoned(sessionId)) {
        store.tombstone(sessionId, owner);
        forgetInMemory(sessionId);
        state.publish({ v: PROTOCOL_VERSION, type: 'session.deleted', sessionId });
        log('info', 'control', 'session tombstoned', { sessionId, deviceId: owner });
      }

      const steps: DeletionStep[] = [];
      if (online) {
        const answer = await askAgent(owner, sessionId, {
          type: 'device.session.delete',
          vendorFile: request.vendorFile,
          // Only a worktree the core recorded is offered; the agent finds it from the session ID.
          worktree: request.worktree && store.sessionWorktree(sessionId) !== null,
          worktreeDirty: request.worktreeDirty,
        });
        if (!answer.ok) {
          log('warn', 'control', 'session delete not answered', {
            sessionId,
            deviceId: owner,
            error: answer.error,
          });
          return {
            ok: false,
            error: answer.error,
            ...(answer.reason !== undefined ? { reason: answer.reason } : {}),
          };
        }
        if (answer.reply.type !== 'device.session.deleted') {
          return { ok: false, error: 'rejected', reason: 'unexpected-reply' };
        }
        steps.push(...answer.reply.steps);
        if (steps.some((s) => s.step === 'stop-syncing' && s.status === 'done')) {
          store.markForgotten(sessionId);
        }
        const failed = steps.some((s) => s.status === 'failed');
        log(failed ? 'warn' : 'info', 'control', 'session delete steps', {
          sessionId,
          deviceId: owner,
          steps: steps.map((s) => `${s.step}:${s.status}`).join(','),
        });
        if (failed) {
          return {
            ok: false,
            error: 'delete-partial',
            steps: [...steps, { step: 'core-copy', status: 'skipped' }],
          };
        }
      } else {
        steps.push({ step: 'stop-syncing', status: 'skipped', reason: 'owner-offline' });
      }

      try {
        store.purgeSession(sessionId);
      } catch (err) {
        log('error', 'control', 'session purge failed', { sessionId, error: String(err) });
        return {
          ok: false,
          error: 'delete-partial',
          steps: [...steps, { step: 'core-copy', status: 'failed', reason: 'remove-failed' }],
        };
      }
      steps.push({ step: 'core-copy', status: 'done' });
      log('info', 'control', 'session deleted', { sessionId, deviceId: owner });
      return { ok: true, steps };
    },

    attached(deviceId) {
      for (const sessionId of store.unforgottenTombstones(deviceId)) {
        void askAgent(deviceId, sessionId, { type: 'device.session.forget' }).then((answer) => {
          if (answer.ok) store.markForgotten(sessionId);
          else log('warn', 'control', 'session forget not answered', { sessionId, deviceId });
        });
      }
    },
  };
}
