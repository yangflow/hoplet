/**
 * Who a request acts as and what that reaches (ADR 0033). The host token is the owner and reaches everything; an API
 * token reaches the abilities and the machines and projects its grant lists. Every check here fails closed.
 */
import {
  grantHas,
  grantSeesDevice,
  grantSeesProject,
  grantSeesWholeDevice,
  type TokenCapability,
} from '@hoplet/shared';
import type { AuditActor, Control } from '../control/index.ts';
import type { TokenRecord } from '../store/index.ts';

/** `link` is a request to a route that carries its own credential (an action link): it reaches nothing else. */
export type Principal = { kind: 'host' } | { kind: 'token'; token: TokenRecord } | { kind: 'link' };

/**
 * What a route requires. `host`: the host token (the default for every route). `link`: the route carries its own
 * credential, a signed action token in the body (ADR 0021), and checks it itself. `{ capability }`: the host token,
 * or an API token whose grant has that ability.
 */
export type Access = 'host' | 'link' | { capability: TokenCapability };

/** Whether the principal has the ability; the host has all of them. */
export function can(principal: Principal, capability: TokenCapability): boolean {
  if (principal.kind === 'token') return grantHas(principal.token.grant, capability);
  return principal.kind === 'host';
}

/** Whether the principal reaches any part of the machine. */
export function seesDevice(principal: Principal, deviceId: string): boolean {
  if (principal.kind === 'token') return grantSeesDevice(principal.token.grant, deviceId);
  return principal.kind === 'host';
}

/** Whether the principal reaches the whole machine, not only listed projects of it. */
export function seesWholeDevice(principal: Principal, deviceId: string): boolean {
  if (principal.kind === 'token') return grantSeesWholeDevice(principal.token.grant, deviceId);
  return principal.kind === 'host';
}

/** Whether the principal reaches the project at `path` of the machine. */
export function seesProject(principal: Principal, deviceId: string, path: string): boolean {
  if (principal.kind === 'token') return grantSeesProject(principal.token.grant, deviceId, path);
  return principal.kind === 'host';
}

/**
 * Whether the principal reaches the session: its owner machine is allowed whole, or its project is. A session
 * without a known owner, or without a known project under a path-limited grant, is not reached.
 */
export function seesSession(
  principal: Principal,
  control: Pick<Control, 'ownerOf' | 'projectOf'>,
  sessionId: string,
): boolean {
  if (principal.kind !== 'token') return principal.kind === 'host';
  // A session always reaches itself (ADR 0034).
  if (principal.token.kind === 'session' && principal.token.sessionId === sessionId) return true;
  const owner = control.ownerOf(sessionId);
  if (!owner) return false;
  if (grantSeesWholeDevice(principal.token.grant, owner)) return true;
  const project = control.projectOf(sessionId);
  return project !== null && grantSeesProject(principal.token.grant, owner, project);
}

/** The audit actor of an action by this principal; `undefined` is the host, which the log records as the operator. */
export function actorOf(principal: Principal): AuditActor | undefined {
  if (principal.kind !== 'token') return undefined;
  return principal.token.kind === 'session'
    ? { kind: 'session', id: principal.token.sessionId }
    : { kind: 'token', id: principal.token.id };
}

/** The session a session token belongs to; null for every other principal. */
export function sessionOf(principal: Principal): string | null {
  return principal.kind === 'token' && principal.token.kind === 'session'
    ? principal.token.sessionId
    : null;
}
