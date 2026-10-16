// One outbound HTTP delivery with a timeout and at most one retry. Nothing here logs or keeps the body.

export type Fetch = typeof fetch;

export type Attempt = { ok: true } | { ok: false; error: string; retry: boolean };

/** Sends one request and classifies the result as the status vocabulary of `NotifyDelivery.error`. */
export async function post(
  fetchImpl: Fetch,
  url: string,
  headers: Record<string, string>,
  body: Uint8Array | string,
  timeoutMs: number,
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
    if (res.status === 404 || res.status === 410) return { ok: false, error: 'gone', retry: false };
    return {
      ok: false,
      error: `http-${res.status}`,
      retry: res.status === 429 || res.status >= 500,
    };
  } catch (err) {
    const timeout =
      err instanceof Error && (err.name === 'TimeoutError' || err.name === 'AbortError');
    return { ok: false, error: timeout ? 'timeout' : 'error', retry: true };
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
