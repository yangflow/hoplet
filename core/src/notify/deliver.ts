// One outbound HTTP delivery with a timeout and at most one retry. Nothing here logs or keeps the body.

export type Fetch = typeof fetch;

export type Attempt = { ok: true } | { ok: false; error: string; retry: boolean };

/** What differs for lifecycle hooks (ADR 0031): they classify and retry by their own rules. */
export interface PostOptions {
  /**
   * Hook mode: 404 and 410 are plain `http-<status>` failures, 408 is retried, a refused address is
   * `blocked-address` (not retried) and a network failure is `network`.
   */
  hook?: boolean;
}

/** A transport refused to connect because the address is not one hooks may call. */
export class BlockedAddressError extends Error {
  constructor() {
    super('blocked-address');
    this.name = 'BlockedAddressError';
  }
}

function causeOf(err: unknown): unknown {
  return err instanceof Error && err.cause !== undefined ? err.cause : err;
}

/** Sends one request and classifies the result as the status vocabulary of `NotifyDelivery.error`. */
export async function post(
  fetchImpl: Fetch,
  url: string,
  headers: Record<string, string>,
  body: Uint8Array | string,
  timeoutMs: number,
  options: PostOptions = {},
): Promise<Attempt> {
  try {
    const res = await fetchImpl(url, {
      method: 'POST',
      headers,
      body: body as BodyInit,
      // A redirect would carry the body to a host the user did not name.
      redirect: 'manual',
      signal: AbortSignal.timeout(timeoutMs),
    });
    await res.body?.cancel().catch(() => {});
    if (res.status >= 200 && res.status < 300) return { ok: true };
    if (!options.hook && (res.status === 404 || res.status === 410)) {
      return { ok: false, error: 'gone', retry: false };
    }
    return {
      ok: false,
      error: `http-${res.status}`,
      retry:
        res.status === 429 || res.status >= 500 || (options.hook === true && res.status === 408),
    };
  } catch (err) {
    if (
      options.hook &&
      (err instanceof BlockedAddressError || causeOf(err) instanceof BlockedAddressError)
    ) {
      return { ok: false, error: 'blocked-address', retry: false };
    }
    const timeout =
      err instanceof Error && (err.name === 'TimeoutError' || err.name === 'AbortError');
    return {
      ok: false,
      error: timeout ? 'timeout' : options.hook ? 'network' : 'error',
      retry: true,
    };
  }
}

/** Runs `attempt`, and once more after `delayMs` when it failed in a way that may pass. */
export async function withRetry(
  attempt: () => Promise<Attempt>,
  delayMs: number,
): Promise<Attempt> {
  const first = await attempt();
  if (first.ok || !first.retry) return first;
  await new Promise((resolve) => setTimeout(resolve, delayMs));
  return attempt();
}
