import type { HttpErrorCode } from '@hoplet/shared';
import { t } from './i18n.ts';

/** A non-2xx REST response. `code` is null when the body was not a Hoplet error response. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: HttpErrorCode | null;
  readonly reason: string | undefined;

  constructor(status: number, code: HttpErrorCode | null, reason?: string) {
    super(code ?? `HTTP ${status}`);
    this.status = status;
    this.code = code;
    this.reason = reason;
  }
}

export class UnauthorizedError extends ApiError {
  constructor() {
    super(401, 'unauthorized');
  }
}

/** The English copy of every error code; the literals are the i18n keys (en.json, zh-Hans.json). */
export const ERROR_COPY: Record<HttpErrorCode, string> = {
  unauthorized: 'The access token was not accepted.',
  'not-found': 'Not found.',
  'method-not-allowed': 'This action is not supported.',
  'session-not-found': 'That session no longer exists.',
  'invalid-json': 'The request was not valid.',
  'invalid-request': 'The request was not valid.',
  'same-device': 'Choose a different machine.',
  'invalid-token': 'The link is not valid.',
  'rate-limited': 'Too many requests. Try again in a moment.',
  'too-large': 'That is too large to send.',
  'owner-offline': 'The machine that owns this session is offline.',
  'target-offline': 'The target machine is offline.',
  busy: 'The session is busy with a turn or a hand-off.',
  'steer-unavailable': 'This turn cannot take a message now. Queue it instead.',
  'queue-conflict': 'That message was already sent, so it can no longer change.',
  'queue-full': 'The queue is full. Wait for a message to be sent first.',
  starting: 'The session is starting. Try again in a moment.',
  external: 'The session was written from a terminal. It can be read but not controlled.',
  'not-running': 'No turn is running.',
  'request-closed': 'That permission request was already answered or has expired.',
  cancelled: 'The request was cancelled.',
  'handoff-in-progress': 'A hand-off of this session is already running.',
  'no-clone': 'That machine has no clone of this repository. Give the path of one.',
  'no-snapshot':
    'The owner is offline and no workspace snapshot was stored. Hand off once it has finished a turn.',
  'session-changed': 'The session changed during the hand-off. Nothing was moved; try again.',
  'verify-failed': "The other machine's copy did not match. Nothing was moved.",
  'session-running': 'Stop the session before deleting it.',
  'delete-partial': 'Some steps failed. Nothing more was removed; delete again to continue.',
  rejected: 'The machine refused the request.',
  timeout: 'The machine did not answer in time.',
  internal: 'Something went wrong on the server.',
};

/**
 * What to show when a machine refuses to add a project: it is offline, or its agent refused the path (it
 * resolves the path and accepts only an existing directory); anything else keeps the copy of its code.
 */
export function addProjectErrorText(err: unknown, device: string): string {
  if (err instanceof ApiError) {
    if (err.code === 'target-offline' || err.code === 'owner-offline')
      return t('{name} is offline').replace('{name}', device);
    if (err.code === 'rejected')
      return t('That path is not a directory on {name}').replace('{name}', device);
  }
  return errorText(err);
}

/**
 * What to show when a machine cannot list a folder: it is offline, did not answer, or its agent refused the
 * path with a reason (`invalid-request`); anything else keeps the copy of its code.
 */
export function browseErrorText(err: unknown, device: string): string {
  const named = (text: string) => t(text).replace('{name}', device);
  if (err instanceof ApiError) {
    if (err.code === 'target-offline' || err.code === 'owner-offline')
      return named('{name} is offline');
    if (err.code === 'timeout') return named('{name} did not answer in time');
    if (err.code === 'invalid-request') {
      switch (err.reason) {
        case 'outside the browse root':
          return named('That folder is outside the browse root on {name}');
        case 'relative path':
          return t('Type an absolute path, or one that starts with ~');
        case 'not a directory':
          return named('That is not a folder on {name}');
        case 'unreadable':
          return named('{name} cannot read that folder');
      }
    }
  }
  return errorText(err);
}

/** What to show for a failed request: the copy of its code, then the agent's reason when it gave one. */
export function errorText(err: unknown): string {
  if (err instanceof ApiError) {
    const copy = err.code ? t(ERROR_COPY[err.code]) : `HTTP ${err.status}`;
    return err.reason && err.reason !== err.code ? `${copy} (${err.reason})` : copy;
  }
  return err instanceof Error ? err.message : String(err);
}
