import {
  type DeviceRules,
  type PermissionRule,
  type PermissionRuleEntry,
  PROTOCOL_VERSION,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import { type ControlState, onlineLink } from './state.ts';
import type { DeviceLink } from './types.ts';

/** How many times the core sends rules again that a device did not confirm, before it waits for a change or a push by hand. */
const MAX_RULE_RETRIES = 5;

/** The rule as the agent reads it: the stored extras (target, timestamps) stay in the core. */
export function body(entry: PermissionRuleEntry): PermissionRule {
  return {
    id: entry.id,
    tool: entry.tool,
    match: entry.match,
    action: entry.action,
    scope: entry.scope,
    note: entry.note,
    enabled: entry.enabled,
    builtin: entry.builtin,
  };
}

/**
 * The permission rules a device enforces (ADR 0029). The core stores them and sends each agent its own set: the
 * global rules with the built-ins, the project rules of that device and the rules of the sessions it owns. The
 * agent decides; it answers every push with the revision it applied.
 */
export function createRules(state: ControlState) {
  const { store } = state;
  /** The content last sent to each device. */
  const sent = new Map<string, string>();
  /** The revision each connected device last applied. */
  const applied = new Map<string, number>();
  /** What each device last confirmed, kept after it disconnects: the revision and when the core heard it. */
  const confirmed = new Map<string, { revision: number; at: number }>();
  /** When the core last sent each device its rules. */
  const pushedAt = new Map<string, number>();
  /** The revision of the last push to each device. */
  const sentRevision = new Map<string, number>();
  /** The pending resend of a push that was not confirmed, and how many resends this push has had. */
  const retryTimers = new Map<string, ReturnType<typeof setTimeout>>();
  const retryCount = new Map<string, number>();
  const now = () => state.now();

  function stopRetry(deviceId: string): void {
    const timer = retryTimers.get(deviceId);
    if (timer) clearTimeout(timer);
    retryTimers.delete(deviceId);
  }

  function scheduleRetry(deviceId: string): void {
    stopRetry(deviceId);
    if ((retryCount.get(deviceId) ?? 0) >= MAX_RULE_RETRIES) return;
    const timer = setTimeout(() => {
      retryTimers.delete(deviceId);
      if (applied.get(deviceId) === sentRevision.get(deviceId)) return;
      retryCount.set(deviceId, (retryCount.get(deviceId) ?? 0) + 1);
      push(deviceId, true, true);
    }, state.rulesRetryMs);
    timer.unref();
    retryTimers.set(deviceId, timer);
  }

  function build(deviceId: string): DeviceRules {
    const global: PermissionRule[] = [];
    const projects: DeviceRules['projects'] = [];
    const sessions: DeviceRules['sessions'] = [];
    for (const entry of store.listPermissionRules()) {
      if (entry.scope === 'global') {
        global.push(body(entry));
      } else if (entry.scope === 'project') {
        if (entry.deviceId === deviceId && entry.projectPath !== undefined) {
          projects.push({ projectPath: entry.projectPath, rule: body(entry) });
        }
      } else if (
        entry.sessionId !== undefined &&
        store.ownerDeviceId(entry.sessionId) === deviceId
      ) {
        sessions.push({ sessionId: entry.sessionId, rule: body(entry) });
      }
    }
    // A child obeys the deny and ask rules of the sessions that started it, and none of their allow rules.
    const inherited: NonNullable<DeviceRules['inherited']> = [];
    for (const [childId, child] of store.sessionChildren()) {
      if (store.ownerDeviceId(childId) !== deviceId) continue;
      for (const entry of store.inheritedPermissionRules(child.chain)) {
        inherited.push({ sessionId: childId, rule: body(entry) });
      }
    }
    return {
      v: PROTOCOL_VERSION,
      type: 'device.rules',
      revision: store.permissionRevision(deviceId),
      global,
      projects,
      sessions,
      ...(inherited.length > 0 ? { inherited } : {}),
    };
  }

  function push(deviceId: string, force: boolean, resend = false): void {
    const link = onlineLink(state, deviceId);
    if (!link) return;
    const msg = build(deviceId);
    const key = JSON.stringify([
      msg.revision,
      msg.global,
      msg.projects,
      msg.sessions,
      msg.inherited ?? [],
    ]);
    if (!force && sent.get(deviceId) === key) return;
    sent.set(deviceId, key);
    if (!resend) retryCount.set(deviceId, 0);
    sentRevision.set(deviceId, msg.revision);
    pushedAt.set(deviceId, now());
    link.send(msg);
    scheduleRetry(deviceId);
  }

  function deviceOf(link: DeviceLink): string | null {
    for (const [deviceId, attached] of state.byDevice) if (attached === link) return deviceId;
    return null;
  }

  return {
    /** A device connected: what it applied and what it was sent belong to the connection that is gone. */
    attached(deviceId: string): void {
      applied.delete(deviceId);
      sent.delete(deviceId);
      stopRetry(deviceId);
    },
    /** The device said hello: its whole set, whether or not it changed. */
    announce(deviceId: string): void {
      push(deviceId, true);
    },
    detached(deviceIds: readonly string[]): void {
      for (const deviceId of deviceIds) {
        sent.delete(deviceId);
        applied.delete(deviceId);
        stopRetry(deviceId);
      }
    },
    /** Sends the named devices (every online device when none are named) the set they do not yet have. A device that has not said hello is sent its whole set at hello. */
    sync(deviceIds?: readonly string[]): void {
      for (const deviceId of deviceIds ?? [...state.byDevice.keys()]) {
        if (sent.has(deviceId)) push(deviceId, false);
      }
    },
    appliedRevision: (deviceId: string): number | null => applied.get(deviceId) ?? null,
    /** Where the rules stand on a device: what it confirmed (kept while it is offline) and what was sent. */
    status(deviceId: string) {
      const last = confirmed.get(deviceId);
      return {
        applied: last?.revision ?? null,
        appliedAt: last?.at ?? null,
        pushedAt: pushedAt.get(deviceId) ?? null,
        retrying: retryTimers.has(deviceId),
      };
    },
    /** Sends the device its whole set again; false when it is not connected or has not said hello. */
    pushAgain(deviceId: string): boolean {
      if (!onlineLink(state, deviceId) || !sent.has(deviceId)) return false;
      retryCount.set(deviceId, 0);
      push(deviceId, true);
      return true;
    },
    receive(link: DeviceLink, revision: number, error: string | undefined): void {
      const deviceId = deviceOf(link);
      if (!deviceId) return;
      if (error !== undefined) {
        // The agent keeps the set it had; the core does not count the refused one as applied, and sending the
        // same set again would be refused again.
        stopRetry(deviceId);
        log('error', 'control', 'permission rules refused by the agent', {
          device: deviceId,
          revision,
          reason: error,
        });
        return;
      }
      applied.set(deviceId, revision);
      confirmed.set(deviceId, { revision, at: now() });
      if (revision >= (sentRevision.get(deviceId) ?? 0)) stopRetry(deviceId);
    },
  };
}
