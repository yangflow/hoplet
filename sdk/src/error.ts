import type { HttpErrorCode } from '@hoplet/shared';

/**
 * Codes the SDK itself raises. `network-error` has status 0 (no response arrived); `invalid-response` is a
 * response that does not match the shared schema; `protocol-version-mismatch` is a core that speaks another
 * WebSocket protocol than this SDK; `aborted` is a wait the caller's `AbortSignal` ended.
 */
export type SdkErrorCode =
  | 'network-error'
  | 'invalid-response'
  | 'protocol-version-mismatch'
  | 'aborted';

/** The code of a failure: a stable core code, an SDK code, or a code a newer core added (passed through). */
export type HopletErrorCode = HttpErrorCode | SdkErrorCode | (string & {});

export type HopletErrorInit = {
  status: number;
  code: HopletErrorCode;
  reason?: string | undefined;
  promptId?: string | undefined;
  message?: string | undefined;
  cause?: unknown;
};

/** Every failure of the SDK. Branch on `code`, never on `message`. */
export class HopletError extends Error {
  /** The HTTP status; 0 when no response arrived. */
  readonly status: number;
  readonly code: HopletErrorCode;
  readonly reason: string | undefined;
  /** The prompt a rejected `prompt` or `new session` request carried, when the core says so. */
  readonly promptId: string | undefined;

  constructor(init: HopletErrorInit) {
    super(
      init.message ?? (init.reason === undefined ? init.code : `${init.code}: ${init.reason}`),
      init.cause === undefined ? undefined : { cause: init.cause },
    );
    this.name = 'HopletError';
    this.status = init.status;
    this.code = init.code;
    this.reason = init.reason;
    this.promptId = init.promptId;
  }
}
