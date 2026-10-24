import { log } from '../log/index.ts';
import type { ControlState } from './state.ts';
import type { PermissionActor } from './types.ts';

/** How a permission request ended, as lifecycle hooks name it (ADR 0031). */
export type PermissionResolution = 'allow' | 'deny' | 'expired' | 'abandoned';

/** Who closed a request nobody answered, for the audit record (ADR 0030): its timer, or the end of its session. */
export type ClosedBy = 'timeout' | 'session-end';

/** The key of an open permission request in `ControlState.permissions`. */
export function permissionKey(sessionId: string, requestId: string): string {
  return `${sessionId}\n${requestId}`;
}

/**
 * A request closed without an answer from a person: the agent denied it, so the record says so. It is best
 * effort, like the record of a rule decision: the request is over either way.
 */
function auditClosure(
  state: ControlState,
  sessionId: string,
  open: { deviceId: string; requestId: string; tool: string },
  closedBy: ClosedBy,
): void {
  try {
    state.store.audit({
      type: 'permission.answered',
      sessionId,
      deviceId: open.deviceId,
      actor: { kind: 'core', id: closedBy === 'timeout' ? 'permission-timeout' : 'session-end' },
      channel: 'core',
      ref: open.requestId,
      summary: { decision: 'deny', answeredBy: closedBy, tool: open.tool },
    });
  } catch (err) {
    log('error', 'control', 'permission closure not recorded', {
      sessionId,
      error: err instanceof Error ? err.message : 'unknown',
    });
  }
}

/**
 * Removes an open permission request and tells lifecycle hooks how it ended. Every place that drops a request
 * goes through here, so a `permission.requested` event always gets its `permission.resolved`. `closedBy` is
 * given when no person answered: the audit log then records the closure.
 */
export function closePermission(
  state: ControlState,
  key: string,
  resolution: PermissionResolution,
  actor: PermissionActor = { kind: 'system' },
  closedBy?: ClosedBy,
): void {
  const open = state.permissions.get(key);
  const timer = state.permissionTimers.get(key);
  if (timer) {
    clearTimeout(timer);
    state.permissionTimers.delete(key);
  }
  if (!open) return;
  state.permissions.delete(key);
  const sessionId = key.slice(0, key.indexOf('\n'));
  if (closedBy) auditClosure(state, sessionId, open, closedBy);
  state.emit({ kind: 'resolved', sessionId, requestId: open.requestId, resolution, actor });
}

/** Closes every open request of a session because the session ended, was deleted or was handed off. */
export function closeSessionPermissions(
  state: ControlState,
  sessionId: string,
  resolution: PermissionResolution,
): void {
  for (const key of [...state.permissions.keys()]) {
    if (key.startsWith(`${sessionId}\n`)) {
      closePermission(state, key, resolution, undefined, 'session-end');
    }
  }
}

/** Arms the expiry of a request that expires: it is closed `expired` when the agent denies it. */
export function armExpiry(state: ControlState, key: string, ms: number): void {
  const timer = setTimeout(() => {
    state.permissionTimers.delete(key);
    closePermission(state, key, 'expired', undefined, 'timeout');
  }, ms);
  timer.unref();
  state.permissionTimers.set(key, timer);
}
