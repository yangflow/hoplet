import { createHmac } from 'node:crypto';

/**
 * The `Hoplet-Signature` header: `t=<unix seconds>,v1=<hex HMAC-SHA-256 of "<t>.<body>">`. While a secret is being
 * rotated the header carries one `v1` per valid secret, newest first.
 */
export function signatureHeader(secrets: readonly string[], body: string, atMs: number): string {
  const t = Math.floor(atMs / 1000);
  const signed = `${t}.${body}`;
  const signatures = secrets.map(
    (s) => `v1=${createHmac('sha256', s).update(signed).digest('hex')}`,
  );
  return [`t=${t}`, ...signatures].join(',');
}
