import type { HttpErrorCode } from '../../shared/src/index.ts';

/** What the CLI prints for each error code. */
export const ERROR_COPY: Record<HttpErrorCode, string> = {
  unauthorized: 'The token was not accepted.',
  'not-found': 'Not found.',
  'method-not-allowed': 'This action is not supported.',
  'session-not-found': 'That session does not exist.',
  'invalid-json': 'The request was not valid.',
  'invalid-request': 'The request was not valid.',
  'same-device': 'Choose a different machine.',
  'invalid-token': 'The link is not valid.',
  'rate-limited': 'Too many requests. Try again in a moment.',
  'too-large': 'That is too large to send.',
  'owner-offline': 'The machine that owns this session is offline.',
  'target-offline': 'The target machine is offline.',
  'over-budget': 'A budget is used up, so new prompts are paused.',
  busy: 'The session is busy with a turn or a hand-off.',
  starting: 'The session is starting. Try again in a moment.',
  external: 'The session was written from a terminal. It can be read but not controlled.',
  'not-running': 'No turn is running.',
  'request-closed': 'That permission request was already answered or has expired.',
  'steer-unavailable':
    'A message cannot be steered into this turn now (no turn is running, a permission waits, or the provider cannot). Queue it instead.',
  'queue-conflict': 'That queued message was already sent, changed or removed.',
  'queue-full': 'The queue is full.',
  cancelled: 'The request was cancelled.',
  'handoff-in-progress': 'A hand-off of this session is already running.',
  'no-clone':
    'That machine has no clone of this repository. Hand off with a path from the hoplet CLI.',
  'handoff-not-running': 'No handoff of this session is cloning now.',
  'clone-failed': 'The repository could not be cloned on the other machine. Nothing was moved.',
  'no-snapshot':
    'The owner is offline and no workspace snapshot was stored. Hand off once it has finished a turn.',
  'session-changed': 'The session changed during the hand-off. Nothing was moved; try again.',
  'verify-failed': "The other machine's copy did not match. Nothing was moved.",
  'session-running': 'The session is running. Stop it first.',
  'terminal-unavailable': 'The terminal is not enabled on that machine.',
  'terminal-busy': 'The session or the machine has too many terminals open.',
  'delete-partial': 'The session was only partly deleted. Run the delete again to finish it.',
  rejected: 'The machine refused the request.',
  timeout: 'The machine did not answer in time.',
  internal: 'Something went wrong on the server.',
};

/** The line to print for a failed request: the copy of its code, then the agent's reason if any. */
export function errorMessage(
  status: number,
  error: { code: HttpErrorCode; reason?: string | undefined } | null,
): string {
  if (!error) return `HTTP ${status}`;
  const copy = ERROR_COPY[error.code];
  return error.reason && error.reason !== error.code ? `${copy} (${error.reason})` : copy;
}
