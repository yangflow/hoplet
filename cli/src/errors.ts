import { HopletError } from '@hoplet/sdk';

/** What the CLI prints for each stable error code; a code it does not know prints as itself. */
const ERROR_COPY: Record<string, string> = {
  unauthorized: 'The token was not accepted.',
  'not-found': 'Not found.',
  'method-not-allowed': 'This action is not supported.',
  'session-not-found': 'That session does not exist.',
  'invalid-json': 'The request was not valid.',
  'invalid-request': 'The request was not valid.',
  'same-device': 'Choose a different machine.',
  'invalid-token': 'The link is not valid.',
  forbidden: 'This access token is not allowed to do that.',
  'rate-limited': 'Too many requests. Try again in a moment.',
  'audit-unavailable': 'The audit log could not record this, so it was not done.',
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
  'no-clone': 'That machine has no clone of this repository. Hand off with --path.',
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
  'network-error': 'The core could not be reached.',
  'invalid-response': 'The core answered something other than the documented shape.',
  'protocol-version-mismatch': 'This command and the core speak different protocol versions.',
  aborted: 'Stopped.',
  'no-token': 'No token. Set HOPLET_TOKEN, HOPLET_TOKEN_FILE or pass --token-file.',
  usage: 'Usage error.',
  'not-supported': 'Not supported.',
};

/** A failure of the command itself, before or apart from any request. */
export class CliError extends Error {
  readonly code: string;
  readonly reason: string | undefined;

  constructor(code: string, reason?: string) {
    super(reason === undefined ? code : `${code}: ${reason}`);
    this.name = 'CliError';
    this.code = code;
    this.reason = reason;
  }
}

export type Failure = { code: string; reason?: string };

/** The code and reason of anything thrown; an error that is neither kind prints as `internal`. */
export function failureOf(err: unknown): Failure {
  if (err instanceof HopletError || err instanceof CliError) {
    return err.reason === undefined ? { code: err.code } : { code: err.code, reason: err.reason };
  }
  return { code: 'internal', reason: err instanceof Error ? err.message : String(err) };
}

/** The line to print for a failure: the copy of its code, then the reason if any. */
export function failureText(failure: Failure): string {
  if ((failure.code === 'usage' || failure.code === 'not-supported') && failure.reason) {
    return failure.reason;
  }
  const copy = ERROR_COPY[failure.code] ?? failure.code;
  return failure.reason && failure.reason !== failure.code ? `${copy} (${failure.reason})` : copy;
}
