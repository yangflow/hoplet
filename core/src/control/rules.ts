import {
  type DeviceRules,
  type PermissionRule,
  type PermissionRuleEntry,
  PROTOCOL_VERSION,
} from '@hoplet/shared';
import { log } from '../log/index.ts';
import { type ControlState, onlineLink } from './state.ts';
import type { DeviceLink } from './types.ts';

/** The rule as the agent reads it: the stored extras (target, timestamps) stay in the core. */
function body(entry: PermissionRuleEntry): PermissionRule {
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
    return {
      v: PROTOCOL_VERSION,
      type: 'device.rules',
      revision: store.permissionRevision(deviceId),
      global,
      projects,
      sessions,
    };
  }

  function push(deviceId: string, force: boolean): void {
    const link = onlineLink(state, deviceId);
    if (!link) return;
    const msg = build(deviceId);
    const key = JSON.stringify([msg.revision, msg.global, msg.projects, msg.sessions]);
    if (!force && sent.get(deviceId) === key) return;
    sent.set(deviceId, key);
    link.send(msg);
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
    },
    /** The device said hello: its whole set, whether or not it changed. */
    announce(deviceId: string): void {
      push(deviceId, true);
    },
    detached(deviceIds: readonly string[]): void {
      for (const deviceId of deviceIds) {
        sent.delete(deviceId);
        applied.delete(deviceId);
      }
    },
    /** Sends the named devices (every online device when none are named) the set they do not yet have. A device that has not said hello is sent its whole set at hello. */
    sync(deviceIds?: readonly string[]): void {
      for (const deviceId of deviceIds ?? [...state.byDevice.keys()]) {
        if (sent.has(deviceId)) push(deviceId, false);
      }
    },
    appliedRevision: (deviceId: string): number | null => applied.get(deviceId) ?? null,
    receive(link: DeviceLink, revision: number, error: string | undefined): void {
      const deviceId = deviceOf(link);
      if (!deviceId) return;
      if (error !== undefined) {
        // The agent keeps the set it had; the core does not count the refused one as applied.
        log('error', 'control', 'permission rules refused by the agent', {
          device: deviceId,
          revision,
          reason: error,
        });
        return;
      }
      applied.set(deviceId, revision);
    },
  };
}
