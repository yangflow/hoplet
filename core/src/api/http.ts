import type { IncomingMessage, ServerResponse } from 'node:http';
import { type ErrorResponse, HTTP_ERROR_STATUS, type HttpErrorCode } from '@hoplet/shared';

export function json(res: ServerResponse, status: number, body: unknown): void {
  const buf = Buffer.from(JSON.stringify(body));
  res.writeHead(status, {
    'content-type': 'application/json; charset=utf-8',
    'content-length': buf.length,
  });
  res.end(buf);
}

/**
 * A response a handler writes itself, such as a stream. The router calls `send` inside the lane the route
 * declares, so a bulk stream holds its place in the bulk queue until the last byte is written.
 */
export class RawResponse {
  readonly send: (res: ServerResponse) => Promise<void>;
  constructor(send: (res: ServerResponse) => Promise<void>) {
    this.send = send;
  }
}

/**
 * Every error response is `{ code }` with the status the code stands for (shared/src/errors.ts);
 * `status` overrides it where an endpoint documents another.
 */
export function fail(
  res: ServerResponse,
  code: HttpErrorCode,
  extra: Omit<ErrorResponse, 'code'> = {},
  status: number = HTTP_ERROR_STATUS[code],
): void {
  json(res, status, { code, ...extra });
}

/** What a handler throws to answer with an error; the router turns it into the response. */
export class ApiError extends Error {
  code: HttpErrorCode;
  extra: Omit<ErrorResponse, 'code'>;
  status: number;
  constructor(
    code: HttpErrorCode,
    extra: Omit<ErrorResponse, 'code'> = {},
    status: number = HTTP_ERROR_STATUS[code],
  ) {
    super(code);
    this.code = code;
    this.extra = extra;
    this.status = status;
  }
}

/** Outcome of a request relayed to an agent, as far as the HTTP mapping is concerned. */
export interface RelayFailure {
  error: HttpErrorCode;
  reason?: string | undefined;
  /** The budget that refused it (`over-budget`). */
  budget?: ErrorResponse['budget'] | undefined;
}

/** Maps a failed relay to its error; `rejected` carries the agent's diagnostic text as `reason`. */
export function relayError(
  outcome: RelayFailure,
  extra: Omit<ErrorResponse, 'code' | 'reason'> = {},
): ApiError {
  if (outcome.error === 'rejected') {
    return new ApiError('rejected', { reason: outcome.reason ?? 'rejected', ...extra });
  }
  if (outcome.error === 'over-budget' && outcome.budget) {
    return new ApiError('over-budget', { budget: outcome.budget, ...extra });
  }
  return new ApiError(outcome.error, extra);
}

/**
 * Reads the request body up to `limit` bytes. Past the limit the rest is discarded unread, so the
 * caller can still answer `too-large` on the open connection.
 */
export function readBody(req: IncomingMessage, limit: number): Promise<string> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let n = 0;
    let exceeded = false;
    req.on('data', (c: Buffer) => {
      if (exceeded) return;
      n += c.length;
      if (n > limit) {
        exceeded = true;
        chunks.length = 0;
        reject(new Error('too-large'));
        return;
      }
      chunks.push(c);
    });
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.on('error', reject);
  });
}
