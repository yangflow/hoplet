import type { DeviceInfo, SessionMcpProjectDefault, SessionMcpSettings } from '@hoplet/shared';
import { MAX_AUTO_APPROVE_MACHINES, type SessionVisibility } from '@hoplet/shared';
import { t } from './i18n.ts';

// What a session may do to other sessions (ADR 0034): the rules the settings follow when one switch moves another.

/** Whether the settings allow sending prompts; starting sessions implies it. */
export const sendOn = (s: SessionMcpSettings): boolean => s.send || s.create;

/** Send turned off also turns Start off, since Start implies Send. */
export function withSend(s: SessionMcpSettings, on: boolean): SessionMcpSettings {
  return on ? { ...s, send: true } : { ...s, send: false, create: false };
}

/** Start turned on turns Send on with it; turned off, Send keeps its own state. */
export function withCreate(s: SessionMcpSettings, on: boolean): SessionMcpSettings {
  return on ? { ...s, create: true, send: true } : { ...s, create: false };
}

/** Hand-off turned off clears automatic approval, which the core refuses without it. */
export function withHandoff(s: SessionMcpSettings, on: boolean): SessionMcpSettings {
  return on
    ? { ...s, requestHandoff: true }
    : { ...s, requestHandoff: false, autoApproveHandoff: false, autoApproveMachines: [] };
}

/** Reading the configuration turned off also turns proposing off, since proposing implies reading (ADR 0036). */
export function withReadConfig(s: SessionMcpSettings, on: boolean): SessionMcpSettings {
  return on ? { ...s, readConfig: true } : { ...s, readConfig: false, writeConfig: false };
}

/** Proposing changes turned on turns reading on with it; turned off, reading keeps its own state. */
export function withWriteConfig(s: SessionMcpSettings, on: boolean): SessionMcpSettings {
  return on ? { ...s, writeConfig: true, readConfig: true } : { ...s, writeConfig: false };
}

export function withAutoApprove(s: SessionMcpSettings, on: boolean): SessionMcpSettings {
  return { ...s, autoApproveHandoff: on && s.requestHandoff };
}

export function withMachine(s: SessionMcpSettings, deviceId: string): SessionMcpSettings {
  if (s.autoApproveMachines.includes(deviceId)) return s;
  if (s.autoApproveMachines.length >= MAX_AUTO_APPROVE_MACHINES) return s;
  return { ...s, autoApproveMachines: [...s.autoApproveMachines, deviceId] };
}

export function withoutMachine(s: SessionMcpSettings, deviceId: string): SessionMcpSettings {
  return { ...s, autoApproveMachines: s.autoApproveMachines.filter((id) => id !== deviceId) };
}

export function withReach(
  s: SessionMcpSettings,
  visibility: SessionVisibility,
): SessionMcpSettings {
  return { ...s, visibility };
}

/** Whether a machine's agent did not announce `session_mcp`: only a connected machine can be known to be off. */
export function mcpOff(device: Pick<DeviceInfo, 'connected' | 'sessionMcp'> | undefined): boolean {
  return device?.connected === true && device.sessionMcp !== true;
}

/** The machines that can still be added to a session's automatic approval. */
export function addableMachines(
  devices: readonly DeviceInfo[],
  listed: readonly string[],
): DeviceInfo[] {
  return devices.filter((d) => !listed.includes(d.deviceId));
}

/** The last part of a path, the way a project is named. */
export function projectName(path: string): string {
  const parts = path.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] ?? path;
}

export function reachName(v: SessionVisibility): string {
  return { project: t('reach.project'), machine: t('reach.machine'), all: t('reach.all') }[v];
}

export function reachSummary(v: SessionVisibility): string {
  return {
    project: t('Same project, same machine'),
    machine: t('Every session on the machine'),
    all: t('Every session on every machine'),
  }[v];
}

/** Project defaults in a stable order: by project name, then machine. */
export function sortedDefaults(
  projects: readonly SessionMcpProjectDefault[],
  devices: readonly DeviceInfo[],
): SessionMcpProjectDefault[] {
  const machine = (id: string) => devices.find((d) => d.deviceId === id)?.name ?? id;
  return [...projects].sort(
    (a, b) =>
      projectName(a.path).localeCompare(projectName(b.path)) ||
      machine(a.deviceId).localeCompare(machine(b.deviceId)) ||
      a.path.localeCompare(b.path),
  );
}
