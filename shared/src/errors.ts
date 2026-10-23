import * as v from 'valibot';
import { DeletionStepSchema } from './deletion.ts';
import { BudgetRefSchema } from './rest/budgets.ts';

/**
 * Stable error codes. A REST error response is `{ code }` (plus `reason` and `promptId` where the endpoint
 * documents them); a WebSocket `error` message carries the same `code`. Clients choose their copy from the
 * code, never from a message or an HTTP status.
 */
export const HTTP_ERROR_STATUS = {
  unauthorized: 401,
  forbidden: 403,
  'not-found': 404,
  'method-not-allowed': 405,
  'session-not-found': 404,
  'invalid-json': 400,
  'invalid-request': 400,
  'same-device': 400,
  self: 400,
  'invalid-token': 400,
  'rate-limited': 429,
  'audit-unavailable': 503,
  'too-large': 413,
  'owner-offline': 409,
  'target-offline': 409,
  'session-running': 409,
  busy: 409,
  starting: 409,
  external: 409,
  'not-running': 409,
  'request-closed': 409,
  'steer-unavailable': 409,
  'queue-conflict': 409,
  'queue-full': 409,
  cancelled: 409,
  'over-budget': 409,
  'handoff-in-progress': 409,
  'loop-limit': 409,
  'depth-limit': 409,
  'children-limit': 409,
  'budget-exceeded': 409,
  'handoff-not-running': 409,
  'terminal-unavailable': 409,
  'terminal-busy': 409,
  'no-clone': 409,
  'no-snapshot': 409,
  'session-changed': 409,
  'verify-failed': 502,
  'clone-failed': 502,
  'delete-partial': 502,
  rejected: 502,
  timeout: 504,
  internal: 500,
} as const;

export type HttpErrorCode = keyof typeof HTTP_ERROR_STATUS;

/** Codes only a WebSocket `error` message carries (it also uses `invalid-json` and `internal`). */
export const WIRE_ONLY_ERROR_CODES = [
  'invalid-message',
  'invalid-session-id',
  'unknown-type',
  'hello-first',
  'device-mismatch',
  'protocol-version-mismatch',
  'snapshot-rejected',
] as const;

export const HTTP_ERROR_CODES = Object.keys(HTTP_ERROR_STATUS) as [
  HttpErrorCode,
  ...HttpErrorCode[],
];

export const ERROR_CODES = [...HTTP_ERROR_CODES, ...WIRE_ONLY_ERROR_CODES] as const;

export const HttpErrorCodeSchema = v.picklist(HTTP_ERROR_CODES);
export const ErrorCodeSchema = v.picklist(ERROR_CODES);
export type ErrorCode = v.InferOutput<typeof ErrorCodeSchema>;

/** The body of every non-2xx REST response. `reason` is diagnostic text from the agent, never copy. */
export const ErrorResponseSchema = v.object({
  code: HttpErrorCodeSchema,
  reason: v.optional(v.string()),
  promptId: v.optional(v.string()),
  /** What `git` said when a clone failed as `git-failed`, with credentials removed and cut at 2 KiB. */
  detail: v.optional(v.string()),
  /** The steps of a deletion that failed half way (`delete-partial`). */
  steps: v.optional(v.array(DeletionStepSchema)),
  /** The budget that refused a prompt (`over-budget`) or a session's start (`budget-exceeded`). */
  budget: v.optional(BudgetRefSchema),
});
export type ErrorResponse = v.InferOutput<typeof ErrorResponseSchema>;
