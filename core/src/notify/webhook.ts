import { createHmac } from 'node:crypto';

/** The `X-Hoplet-Signature` value: `sha256=` and the hex HMAC-SHA-256 of the body. */
export function signBody(secret: string, body: string | Uint8Array): string {
  return `sha256=${createHmac('sha256', secret).update(body).digest('hex')}`;
}

/** The headers of a webhook delivery; it is signed when the webhook has a secret. */
export function webhookHeaders(
  body: string,
  secret: string | null,
  event: string,
): Record<string, string> {
  return {
    'content-type': 'application/json',
    'user-agent': 'Hoplet-Webhook',
    'x-hoplet-event': event,
    ...(secret ? { 'x-hoplet-signature': signBody(secret, body) } : {}),
  };
}
